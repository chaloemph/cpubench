package control

import (
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

func TestAutoDispatchOneAtATime(t *testing.T) {
	b := NewBoard()
	b.Seen(Node{Name: "ts1"}, t0)
	b.AddAuto("j1", "ts1", "quick", t0)
	b.AddAuto("j2", "ts1", "full", t0)
	if j := b.Dispatch("ts1", t0); j == nil || j.ID != "j1" {
		t.Fatalf("%+v", j)
	}
	if j := b.Dispatch("ts1", t0); j != nil {
		t.Fatal("dispatched while busy")
	}
	b.Update("j1", Status{State: Done, ResultID: "r1"}, t0)
	if j := b.Dispatch("ts1", t0); j == nil || j.ID != "j2" {
		t.Fatalf("%+v", j)
	}
	if b.Job("j1").ResultID != "r1" {
		t.Fatal("result id lost")
	}
}

func TestWebPairing(t *testing.T) {
	b := NewBoard()
	s, l := b.AddWebTest("s1", "l1", "arm-01", "lg1", "quick", "http://10.0.0.5:8080", t0)
	if l.State != Waiting || l.Machine != "arm-01" || b.Dispatch("lg1", t0) != nil {
		t.Fatal("load must wait for the server")
	}
	if j := b.Dispatch("arm-01", t0); j == nil || j.ID != s.ID {
		t.Fatal("serve not dispatched")
	}
	wake := b.Update("s1", Status{State: Running, Stage: ReadyStage}, t0)
	if !slices.Equal(wake, []string{"lg1"}) || b.Job("l1").State != Queued {
		t.Fatalf("load not released: %v %+v", wake, b.Job("l1"))
	}
	if j := b.Dispatch("lg1", t0); j == nil || j.Label != "arm-01" || j.Target == "" {
		t.Fatalf("%+v", j)
	}
	wake = b.Update("l1", Status{State: Done, ResultID: "r"}, t0)
	if !slices.Equal(wake, []string{"arm-01"}) || !b.Job("s1").CancelRequested {
		t.Fatal("server not asked to stop after load finished")
	}
	if got := b.Cancels("arm-01", []string{"s1"}); !slices.Equal(got, []string{"s1"}) {
		t.Fatalf("cancels %v", got)
	}
	b.Update("s1", Status{State: Canceled}, t0)
	if b.Active() {
		t.Fatal("board should be idle")
	}
}

func TestServerFailureCancelsLoad(t *testing.T) {
	b := NewBoard()
	b.AddWebTest("s1", "l1", "a", "b", "quick", "http://a:8080", t0)
	b.Dispatch("a", t0)
	b.Update("s1", Status{State: Failed, Message: "port in use"}, t0)
	if b.Job("l1").State != Canceled {
		t.Fatalf("%+v", b.Job("l1"))
	}
}

func TestCancelQueuedAndRunning(t *testing.T) {
	b := NewBoard()
	b.AddAuto("q", "n", "quick", t0)
	b.Cancel("q", t0)
	if b.Job("q").State != Canceled {
		t.Fatal("queued not cancelled")
	}
	b.AddAuto("r", "n", "quick", t0)
	b.Dispatch("n", t0)
	if wake := b.Cancel("r", t0); !slices.Equal(wake, []string{"n"}) || !b.Job("r").CancelRequested {
		t.Fatal("running job not flagged")
	}
	if got := b.Cancels("n", []string{"r", "ghost"}); len(got) != 2 {
		t.Fatalf("%v", got)
	}
}

func TestVMNodeKeepsHostMachine(t *testing.T) {
	b := NewBoard()
	b.Seen(Node{Name: "arm-01-vm", Machine: "arm-01"}, t0)
	b.Seen(Node{Name: "lg1"}, t0)
	if j := b.AddAuto("a", "arm-01-vm", "quick", t0); j.Label != "arm-01-vm" || j.Machine != "arm-01" {
		t.Fatalf("%+v", j)
	}
	if _, l := b.AddWebTest("s", "l", "arm-01-vm", "lg1", "quick", "http://x", t0); l.Label != "arm-01-vm" || l.Machine != "arm-01" {
		t.Fatalf("%+v", l)
	}
}

func TestReconcileLostJob(t *testing.T) {
	b := NewBoard()
	b.AddWebTest("s", "l", "pod", "lg", "quick", "http://x", t0)
	b.Dispatch("pod", t0)
	if wake := b.Reconcile("pod", []string{"s"}, t0); len(wake) != 0 || b.Job("s").State != Running {
		t.Fatal("job still reported must stay running")
	}
	b.Reconcile("pod", nil, t0) // pod restarted
	if b.Job("s").State != Failed || b.Job("l").State != Canceled {
		t.Fatalf("%+v %+v", b.Job("s"), b.Job("l"))
	}
}

func TestOnline(t *testing.T) {
	n := Node{LastSeen: t0}
	if !n.Online(t0.Add(30*time.Second)) || n.Online(t0.Add(2*time.Minute)) {
		t.Fatal("online window")
	}
}
