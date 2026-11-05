package compare

import (
	"math"

	"github.com/chaloemph/cpubench/internal/domain/result"
)

// TCOInput is the cost model for an on-prem comparison.
type TCOInput struct {
	TargetRPS  float64                `json:"target_rps" yaml:"target_rps"`
	Redundancy int                    `json:"redundancy" yaml:"redundancy"` // extra servers (N+x)
	Servers    map[string]result.Cost `json:"servers" yaml:"servers"`       // keyed by result label
}

// Only keeps the servers of the given files: a copy sent to one vendor must
// not carry the other vendors' prices.
func (in *TCOInput) Only(files []*result.File) *TCOInput {
	out := *in
	out.Servers = map[string]result.Cost{}
	for _, f := range files {
		if c, ok := in.Servers[f.Label]; ok {
			out.Servers[f.Label] = c
		}
	}
	return &out
}

type TCO struct {
	TargetRPS  float64  `json:"target_rps"`
	Redundancy int      `json:"redundancy"`
	Rows       []TCORow `json:"rows"`
}

type TCORow struct {
	Label       string  `json:"label"`
	Currency    string  `json:"currency"`
	KneeRPS     float64 `json:"knee_rps"`
	Watts       float64 `json:"watts"`
	RPSPerWatt  float64 `json:"rps_per_watt"`
	Servers     int     `json:"servers"`
	Capex       float64 `json:"capex"`
	EnergyPerYr float64 `json:"energy_per_year"`
	Total       float64 `json:"total"`
	Years       float64 `json:"years"`
	Note        string  `json:"note,omitempty"`
}

// ComputeTCO sizes each candidate for TargetRPS from its measured knee and
// power, then adds purchase price and energy over the server lifespan.
func ComputeTCO(files []*result.File, in TCOInput) *TCO {
	if len(in.Servers) == 0 {
		return nil
	}
	out := &TCO{TargetRPS: in.TargetRPS, Redundancy: in.Redundancy}
	for _, f := range files {
		c, ok := in.Servers[f.Label]
		if !ok {
			continue
		}
		row := TCORow{Label: f.Label, Currency: c.Currency, Years: c.LifespanYears}
		if f.Web != nil {
			row.KneeRPS = f.Web.KneeRPS
			for _, s := range f.Web.Steps {
				if s.PassSLO && s.AchievedRPS == f.Web.KneeRPS && s.Watts > 0 {
					row.Watts = s.Watts
				}
			}
		}
		if c.Watts > 0 {
			row.Watts = c.Watts
		}
		if row.Watts == 0 && f.Power != nil {
			row.Watts = f.Power.Avg
		}
		if row.KneeRPS <= 0 {
			row.Note = "no web knee measured"
			out.Rows = append(out.Rows, row)
			continue
		}
		if row.Watts > 0 {
			row.RPSPerWatt = row.KneeRPS / row.Watts
		}
		pue := c.PUE
		if pue <= 0 {
			pue = 1
		}
		row.Servers = int(math.Ceil(in.TargetRPS/row.KneeRPS)) + in.Redundancy
		row.Capex = float64(row.Servers) * c.ServerPrice
		row.EnergyPerYr = float64(row.Servers) * row.Watts * pue * 8760 / 1000 * c.PowerRate
		row.Total = row.Capex + row.EnergyPerYr*c.LifespanYears
		if row.Watts == 0 {
			row.Note = "no power data: energy cost is 0"
		}
		out.Rows = append(out.Rows, row)
	}
	return out
}
