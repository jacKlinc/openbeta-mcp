# What `MaxCrags` costs

**Measured 2026-08-19 against a local openbeta-graphql stack, three sweeps of the
rebuilt corpus at `MaxCrags` 10, 20 and 40. Corpus and method in
[../corpus/README.md](../corpus/README.md).**

## The numbers

p95, which is the figure to budget against — the median flatters a tool whose
job is occasionally to return a lot.

| `MaxCrags` | \_                      | `crags_near`  | `find_climbs` | `get_area_details` |
| ---------- | ----------------------- | ------------- | ------------- | ------------------ |
| 10         | tokens p50 / p95        | 884 / 1,076   | 1,221 / 3,376 | 513 / 3,519        |
| 20         |                         | 1,580 / 2,078 | 1,858 / 3,761 | 513 / 3,519        |
| 40         |                         | 2,157 / 3,878 | 2,473 / 3,761 | 513 / 3,519        |
| 10         | round trips p95 / total | 11 / 286      | 11 / 286      | 1 / 80             |
| 20         |                         | 21 / 484      | 21 / 484      | 1 / 80             |
| 40         |                         | 41 / 772      | 41 / 772      | 1 / 80             |
| 10         | latency p95 (ms)        | 57            | 40            | 6                  |
| 20         |                         | 62            | 73            | 6                  |
| 40         |                         | 96            | 98            | 6                  |

`get_area_details` takes no part in the cap and does not move, which is the
control: the sweep is not perturbing anything it should not.

## The two tools respond differently, and that is the finding

**`crags_near` is linear.** Tokens track the cap almost exactly — roughly 90-100
per crag admitted, p95 rising 1,076 → 2,078 → 3,878. The cap *is* the cost dial
for this tool. Nothing surprising, but now measured rather than assumed.

**`find_climbs` saturates.** Its p95 goes 3,376 → 3,761 → 3,761: flat from 20 to
40, while the round trips behind it nearly triple, 484 → 772. `MaxClimbs = 30`
([find_climbs.go:17-18](../../../internal/tools/find_climbs.go#L17-L18)) bounds
the returned routes, so past a point the extra crags are scanned, their climbs
fetched and filtered, and then discarded before the payload is built.

Doubling the cap from 20 to 40 therefore buys a `find_climbs` caller **nothing
at the tail** and costs the API 60% more requests. Only the median moves
(1,858 → 2,473), from calls that were not hitting `MaxClimbs` anyway.

There is a cheaper version of the same tool available: at cap 10, `find_climbs`
delivers a p95 of 3,376 — 90% of the p95 at cap 20 — for 59% of the upstream
requests.

## Conclusion: leave the cap at 20, and do not optimise tokens yet

Three reasons, in order of weight.

**1. 4k tokens is not a problem worth solving.** A p95 of 3.8k on the most
expensive tool is a small fraction of any modern context window, and the tools
are called a handful of times per conversation, not in a loop. There is no
budget being blown. Optimising it now would be work with no measurable
beneficiary.

**2. The cost curve is useless without a quality curve.** The sweep says what
each cap *costs*. It cannot say what each cap is *worth*, and the interesting
question — does a model answer a climbing question better with 40 crags than
with 10? — is exactly what the judge measures. Choosing a point on a cost curve
without the quality curve beside it is guessing with extra steps.

The prize is real: if 10 crags answers as well as 20, the cap halves the token
cost and cuts upstream load by 41%, on a volunteer-run API. That is a decision
the judge can support and this sweep cannot.

**3. Latency is not in contention.** 40 to 98 ms p95 across every tool and every
cap, locally. Even allowing an order of magnitude for the public API and the
network, nothing here is a user-visible wait.

So: **on to the judge.** No further cost experiments are needed first.

## What to run when the judge exists

The one experiment worth queueing, because it needs both halves and neither is
useful alone: the same 10/20/40 sweep with the judge scoring the answers, giving
cost against quality per cap. `MaxCrags` is already settable through
`OPENBETA_MAX_CRAGS` and already lands in MLflow as a run param, so the sweep
side of that is done.

Two smaller things this turned up, neither blocking:

- **`find_climbs` should probably stop early.** It has a `MaxClimbs = 30` bound
  it could check while scanning, rather than fetching every crag's climbs and
  discarding the surplus. That is a real saving against the API at no cost to
  the caller. It is a server change, not an eval one.
- **`crags_near`'s `count` cannot keep its promise.** The schema tells the model
  the count "may exceed the crags array", but the code sets it to the length of
  the truncated, filtered list, so it never can. A model cannot distinguish 20
  crags nearby from 500. See [../corpus/README.md](../corpus/README.md) §3.
