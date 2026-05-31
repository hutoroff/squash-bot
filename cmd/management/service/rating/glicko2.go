// Package rating implements the Glicko-2 rating algorithm.
// Reference: Mark Glickman, "Example of the Glicko-2 system" (2013)
// http://www.glicko.net/glicko/glicko2.pdf
package rating

import "math"

const (
	// DefaultRating is the initial rating for a new player.
	DefaultRating = 1500.0
	// DefaultRD is the initial rating deviation for a new player.
	DefaultRD = 350.0
	// DefaultVolatility is the initial volatility.
	DefaultVolatility = 0.06
	// Tau is the system constant controlling volatility change.
	Tau = 0.5
	// ε is the convergence tolerance for the Illinois algorithm.
	epsilon = 0.000001
	// scaleToGlicko2 converts from Glicko-1 scale to Glicko-2 scale.
	scaleToGlicko2 = 173.7178
	// minRD and maxRD clamp RD defensively.
	minRD = 30.0
	maxRD = 350.0
)

// Rating holds a player's Glicko-2 state.
type Rating struct {
	R      float64 // rating (Glicko-1 scale, e.g. 1500)
	RD     float64 // rating deviation (Glicko-1 scale, e.g. 200)
	Sigma  float64 // volatility (e.g. 0.06)
}

// Default returns initial rating values for a new player.
func Default() Rating {
	return Rating{R: DefaultRating, RD: DefaultRD, Sigma: DefaultVolatility}
}

// MatchResult represents one opponent's rating and the score outcome.
type MatchResult struct {
	Opponent Rating
	Score    float64 // 1 = win, 0.5 = draw, 0 = loss
}

// Apply runs the Glicko-2 update for a player who played the given results in one period.
// tau is the system constant (typically 0.5). If results is empty, RD increases (no games played).
func Apply(p Rating, results []MatchResult, tau float64) Rating {
	if len(results) == 0 {
		// No matches: only RD increases (uncertainty grows).
		newRD := math.Sqrt(p.RD*p.RD + p.Sigma*p.Sigma)
		if newRD > maxRD {
			newRD = maxRD
		}
		return Rating{R: p.R, RD: newRD, Sigma: p.Sigma}
	}

	// Step 2: convert to Glicko-2 scale.
	mu := (p.R - 1500.0) / scaleToGlicko2
	phi := p.RD / scaleToGlicko2

	// Precompute g and E for each result.
	type computed struct {
		g, e, s float64
	}
	comps := make([]computed, len(results))
	for i, r := range results {
		muJ := (r.Opponent.R - 1500.0) / scaleToGlicko2
		phiJ := r.Opponent.RD / scaleToGlicko2
		gJ := gFunc(phiJ)
		eJ := eFunc(mu, muJ, phiJ)
		comps[i] = computed{g: gJ, e: eJ, s: r.Score}
	}

	// Step 3: compute v (estimated variance).
	v := 0.0
	for _, c := range comps {
		v += c.g * c.g * c.e * (1 - c.e)
	}
	v = 1.0 / v

	// Step 4: compute delta (improvement).
	delta := 0.0
	for _, c := range comps {
		delta += c.g * (c.s - c.e)
	}
	delta *= v

	// Step 5: update volatility using Illinois algorithm.
	a := math.Log(p.Sigma * p.Sigma)
	x := illinoisIter(a, delta, phi, v, tau)
	sigmaNew := math.Exp(x / 2.0)

	// Step 6: update phi* (pre-rating RD).
	phiStar := math.Sqrt(phi*phi + sigmaNew*sigmaNew)

	// Step 7: update phi' and mu'.
	phiNew := 1.0 / math.Sqrt(1.0/(phiStar*phiStar)+1.0/v)
	sumge := 0.0
	for _, c := range comps {
		sumge += c.g * (c.s - c.e)
	}
	muNew := mu + phiNew*phiNew*sumge

	// Step 8: convert back to Glicko-1 scale.
	rNew := scaleToGlicko2*muNew + 1500.0
	rdNew := phiNew * scaleToGlicko2

	// Defensive clamp on RD.
	rdNew = math.Max(minRD, math.Min(maxRD, rdNew))

	return Rating{R: rNew, RD: rdNew, Sigma: sigmaNew}
}

func gFunc(phi float64) float64 {
	return 1.0 / math.Sqrt(1+3*phi*phi/(math.Pi*math.Pi))
}

func eFunc(mu, muJ, phiJ float64) float64 {
	return 1.0 / (1 + math.Exp(-gFunc(phiJ)*(mu-muJ)))
}

// illinoisIter finds the new log-volatility via the Illinois algorithm (Glicko-2 step 5).
func illinoisIter(a, delta, phi, v, tau float64) float64 {
	A := a
	f := func(x float64) float64 {
		ex := math.Exp(x)
		d2 := delta * delta
		phi2 := phi * phi
		num := ex * (d2 - phi2 - v - ex)
		den := 2 * math.Pow(phi2+v+ex, 2)
		return num/den - (x-a)/(tau*tau)
	}

	B := 0.0
	if delta*delta > phi*phi+v {
		B = math.Log(delta*delta - phi*phi - v)
	} else {
		k := 1
		for f(a-float64(k)*tau) < 0 {
			k++
		}
		B = a - float64(k)*tau
	}

	fA, fB := f(A), f(B)
	for math.Abs(B-A) > epsilon {
		C := A + (A-B)*fA/(fB-fA)
		fC := f(C)
		if fC*fB <= 0 {
			A = B
			fA = fB
		} else {
			fA /= 2
		}
		B = C
		fB = fC
	}
	return A
}

