package main

import "strings"

// A worker's totals are its result line's, and a session the factory ends (on the deadline, a stop,
// a cancel) never prints one. For that case the factory counts the totals itself from the assistant
// lines it reads anyway, and prices them with the list prices below. The worker's own figure wins
// whenever there is one: it is what Claude Code billed, while the factory's count is a floor, because
// the output tokens of a message are not all known when its lines are printed.

// price is what a model costs per million tokens, in US dollars. A cache write is the input price
// times 1.25 for the five-minute cache and times 2 for the hour, which the usage tells apart.
type price struct {
	input, output, cacheRead float64
}

// prices are the list prices of the Claude API, as the claude-api reference gave them on 2026-09-23,
// by the prefix of the model id; the first prefix that matches decides, so a longer id stands before
// the shorter one it begins with. A model that is in none of them is counted in tokens and turns and
// left out of the cost, and the run says so.
var prices = []struct {
	prefix string
	price  price
}{
	{"claude-fable-5-1", price{10, 50, 0.25}},
	{"claude-mythos-5-1", price{10, 50, 0.25}},
	{"claude-fable-5", price{10, 50, 1}},
	{"claude-mythos-5", price{10, 50, 1}},
	{"claude-opus-5-5", price{4, 20, 0.2}},
	{"claude-opus-5", price{5, 25, 0.5}},
	{"claude-opus-4-8", price{5, 25, 0.5}},
	{"claude-opus-4-7", price{5, 25, 0.5}},
	{"claude-opus-4-6", price{5, 25, 0.5}},
	{"claude-opus-4-5", price{5, 25, 0.5}},
	{"claude-sonnet-5", price{2, 10, 0.2}},
	{"claude-sonnet-4", price{3, 15, 0.3}},
	{"claude-haiku-4-5", price{1, 5, 0.1}},
}

func priceOf(model string) (price, bool) {
	for _, p := range prices {
		if strings.HasPrefix(model, p.prefix) {
			return p.price, true
		}
	}
	return price{}, false
}

// cost is what these tokens cost at that price, in US dollars.
func (p price) cost(u usage) float64 {
	hour := u.CacheCreationDetail.Hour
	if hour > u.CacheCreation {
		hour = u.CacheCreation
	}
	written := float64(u.CacheCreation-hour)*p.input*1.25 + float64(hour)*p.input*2
	return (float64(u.Input)*p.input + written + float64(u.CacheRead)*p.cacheRead + float64(u.Output)*p.output) / 1e6
}

// tally counts one assistant line into the run's totals. A message comes as one assistant line per
// content block, all with its id, and a later line carries at least what an earlier one did: a
// message the run has not seen is one more turn when it is the worker's own, and one it has seen adds
// only what its usage grew by. What it adds is added to the session's count as well, which its result
// line's totals replace. A model with no price here is noted, for the run's end to say whether the
// cost it records leaves one out. Callers hold the lock.
func (r *Run) tally(session *heard, id, model string, sub bool, u usage) {
	if r.counted == nil {
		r.counted = map[string]usage{}
	}
	before, seen := r.counted[id]
	if id == "" {
		seen, before = false, usage{} // a line without an id is a message of its own
	}
	now := before.atLeast(u)
	if id != "" {
		r.counted[id] = now
	}
	grown := now.minus(before)
	for _, t := range []*Tokens{&r.Tokens, &session.tokens} {
		t.Input += grown.Input
		t.Output += grown.Output
		t.CacheCreation += grown.CacheCreation
		t.CacheRead += grown.CacheRead
	}
	if !seen && !sub {
		r.Turns++
		session.turns++
	}
	r.Totals = totalsFactory
	// A message Claude Code writes itself, such as an API error, names no model the API has and
	// carries no usage: there is nothing to price.
	if p, ok := priceOf(model); ok {
		r.CostUSD += p.cost(grown)
		session.cost += p.cost(grown)
	} else if grown != (usage{}) {
		if r.unpricedModels == nil {
			r.unpricedModels = map[string]bool{}
		}
		r.unpricedModels[model] = true
	}
}

// atLeast is the larger of two usages, field by field.
func (u usage) atLeast(o usage) usage {
	return usage{Input: max(u.Input, o.Input), CacheCreation: max(u.CacheCreation, o.CacheCreation),
		CacheRead: max(u.CacheRead, o.CacheRead), Output: max(u.Output, o.Output),
		CacheCreationDetail: cacheCreation{Hour: max(u.CacheCreationDetail.Hour, o.CacheCreationDetail.Hour)}}
}

func (u usage) minus(o usage) usage {
	return usage{Input: u.Input - o.Input, CacheCreation: u.CacheCreation - o.CacheCreation,
		CacheRead: u.CacheRead - o.CacheRead, Output: u.Output - o.Output,
		CacheCreationDetail: cacheCreation{Hour: u.CacheCreationDetail.Hour - o.CacheCreationDetail.Hour}}
}
