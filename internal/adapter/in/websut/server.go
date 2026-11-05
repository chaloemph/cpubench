// Package websut is the HTTP server cpubench puts under load: a small web
// API whose endpoints exercise the CPU paths real web services spend time in
// (JSON, templates, gzip, password hashing, locked caches, TLS).
package websut

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"math/big"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/chaloemph/cpubench/internal/port"
)

type Server struct {
	Probe port.SystemProbe
	// Power, when set, is reported on /_cpubench/stats?power=1 so a load
	// generator on another machine can attribute watts to each load step.
	Power port.PowerMeter
	// TLSKey is "" (plain HTTP), "ecdsa" or "rsa" for a self-signed certificate.
	TLSKey string

	cacheMu sync.RWMutex
	cache   map[int]string
	page    []byte
}

type product struct {
	ID          int               `json:"id"`
	SKU         string            `json:"sku"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Price       float64           `json:"price"`
	Currency    string            `json:"currency"`
	Stock       int               `json:"stock"`
	Tags        []string          `json:"tags"`
	Attributes  map[string]string `json:"attributes"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

func makeProduct(id int) product {
	return product{ID: id, SKU: fmt.Sprintf("SKU-%06d", id), Name: fmt.Sprintf("Product %d", id),
		Description: strings.Repeat("Comfortable, durable and made to last. ", 8), Price: float64(id%10000) / 100,
		Currency: "THB", Stock: id % 97, Tags: []string{"new", "sale", "popular"},
		Attributes: map[string]string{"color": "blue", "size": "M", "material": "cotton"},
		UpdatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

var listTmpl = template.Must(template.New("list").Parse(`<!doctype html><html><head><title>Products</title></head><body>
<h1>Products</h1><table>{{range .}}<tr><td>{{.SKU}}</td><td>{{.Name}}</td><td>{{printf "%.2f" .Price}} {{.Currency}}</td><td>{{.Description}}</td></tr>{{end}}</table></body></html>`))

func (s *Server) Handler() http.Handler {
	s.cache = map[int]string{}
	for i := range 10000 {
		s.cache[i] = fmt.Sprintf("session-%d", i)
	}
	var page bytes.Buffer
	for page.Len() < 50<<10 {
		fmt.Fprintf(&page, "<li>item %d: %s</li>\n", page.Len(), strings.Repeat("lorem ipsum ", 4))
	}
	s.page = page.Bytes()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /plaintext", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("Hello, World!"))
	})
	mux.HandleFunc("GET /json", func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.Atoi(r.URL.Query().Get("id"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(makeProduct(id))
	})
	mux.HandleFunc("POST /json/parse", func(w http.ResponseWriter, r *http.Request) {
		var items []product
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&items); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var total float64
		for _, it := range items {
			total += it.Price * float64(it.Stock)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"items":%d,"total":%.2f}`, len(items), total)
	})
	mux.HandleFunc("GET /html", func(w http.ResponseWriter, r *http.Request) {
		items := make([]product, 20)
		for i := range items {
			items[i] = makeProduct(i)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		listTmpl.Execute(w, items)
	})
	mux.HandleFunc("GET /gzip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Content-Encoding", "gzip")
		zw, _ := gzip.NewWriterLevel(w, gzip.DefaultCompression)
		zw.Write(s.page)
		zw.Close()
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		user := r.FormValue("user")
		key, err := pbkdf2.Key(sha256.New, "password-"+user, []byte("cpubench-salt-"+user), 10000, 32)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q}`, base64.RawURLEncoding.EncodeToString(key))
	})
	mux.HandleFunc("GET /cache", func(w http.ResponseWriter, r *http.Request) {
		k, _ := strconv.Atoi(r.URL.Query().Get("key"))
		k %= 10000
		if r.URL.Query().Get("write") == "1" {
			s.cacheMu.Lock()
			s.cache[k] = fmt.Sprintf("session-%d-%d", k, time.Now().UnixNano())
			s.cacheMu.Unlock()
		}
		s.cacheMu.RLock()
		v := s.cache[k]
		s.cacheMu.RUnlock()
		w.Write([]byte(v))
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /_cpubench/stats", func(w http.ResponseWriter, r *http.Request) {
		var ru syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
		cpu := float64(ru.Utime.Sec+ru.Stime.Sec) + float64(ru.Utime.Usec+ru.Stime.Usec)/1e6
		st := struct {
			CPUSeconds float64 `json:"cpu_seconds"`
			NumCPU     int     `json:"num_cpu"`
			Watts      float64 `json:"watts,omitempty"`
			WattsErr   string  `json:"watts_error,omitempty"`
		}{CPUSeconds: cpu, NumCPU: runtime.GOMAXPROCS(0)}
		if r.URL.Query().Get("power") == "1" && s.Power != nil {
			if wt, err := s.Power.Watts(r.Context()); err == nil {
				st.Watts = wt
			} else {
				st.WattsErr = err.Error()
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(st)
	})
	mux.HandleFunc("GET /_cpubench/info", func(w http.ResponseWriter, r *http.Request) {
		sys, err := s.Probe.System(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(sys)
	})
	return mux
}

// Serve listens on addr until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, addr string) error {
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if s.TLSKey != "" {
		cert, err := selfSigned(s.TLSKey)
		if err != nil {
			return err
		}
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
		ln = tls.NewListener(ln, srv.TLSConfig)
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func selfSigned(kind string) (tls.Certificate, error) {
	var key any
	var pub any
	switch kind {
	case "rsa":
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return tls.Certificate{}, err
		}
		key, pub = k, &k.PublicKey
	case "ecdsa":
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return tls.Certificate{}, err
		}
		key, pub = k, &k.PublicKey
	default:
		return tls.Certificate{}, fmt.Errorf("unknown TLS key type %q (ecdsa or rsa)", kind)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "cpubench"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		DNSNames: []string{"localhost"}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
