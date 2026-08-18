# Stage 6: Eval design

Measuring token and API cost of MCP tool response shapes, and whether trimming
those shapes hurts task success.

Companion to `openbeta-mcp-plan.md`. Repo: https://github.com/jacKlinc/openbeta-mcp

File structure, tooling and harness mechanics live in `eval-implementation.md`.
This doc is methodology only.

## The thesis

Token count alone is a script, not a project. "I removed fields and it used fewer
tokens" is a foregone conclusion. The version worth building has a second axis:
**does trimming hurt task success?** Trim hard enough and the model can no longer
answer, and a pure token metric rewards exactly that. Tokens against correctness
is an eval harness, which is the target skillset and a far better artifact than a
token counter.

## Split the study in two

The token cost of a response shape is deterministic. It does not need inference to
measure. Keeping these halves separate means the interesting tail study is not
bottlenecked on inference budget.

### Half one: payload sizing (cheap, no model calls)

Serialize tool responses across many bboxes and densities, run them through a
token counter, plot the distribution.

- Squamish (22 areas, known baseline), Yosemite, all of California, zoom levels
that pull the entire hierarchy.
- Output: distribution with a p99 and a stated breaking point.
- Zero model calls, so thousands of shapes are free and the density sweep can be
genuinely broad.

This is the half that actually supports the RFC's pagination requirement. It turns
"we should paginate" into "paginate at N, and here is why".

### Half two: agent eval (expensive, real inference)

Task success across response variants. Smaller question set, real cost. Variants
and judges live here.

## Grading: judge is the wrong default

Most of the question set has computable ground truth derived straight from the
API. Reaching for a judge on those adds cost, latency, and a second source of
error to measure something checkable with `==`.

Three tiers, in order of preference:

**1. Deterministic.** Ask for a parseable final answer (JSON list of area names,
a number).
- Scalars: exact match.
- Lists: set F1, so partial credit is visible.
- **Report precision and recall separately.** A variant that drops crags is a
recall failure; one that invents them is a precision failure. Different bugs.
Averaging them into one score hides which is happening.

**2. Normalized string matching.** Area names need it. "The Smoke Bluffs" vs
"Smoke Bluffs" is not a wrong answer. Case fold, strip leading articles, match.

**3. Judge.** Only where output is prose and correctness means faithfulness
rather than accuracy.

## Where a judge earns its place

The real failure mode of over-trimming is not that the model cannot answer. It is
that it makes something up. Strip `totalClimbs`, ask about route counts, and the
model may produce a plausible number rather than saying it does not know. Set F1
on answerable questions will not catch this, because those questions are
answerable.

So: add a subset of open questions ("what's the climbing like at Smoke Bluffs")
and judge **groundedness**. Is every factual claim supported by what the tool
actually returned?

The judge sees: the question, the exact tool output the model had in context, and
the answer. **Not** the ground truth, because this is checking entailment against
the context, not correctness against the world.

Design rules:

- Binary or three-point. A 1 to 10 scale is noise dressed as precision.
- Rubric plus two or three few-shot examples in the judge prompt.
- Different model than the one under test. Self-preference bias is well
documented and should not confound a trimming study.
- **Validate the judge.** Hand-label 30 examples, report agreement: [Cohen's
kappa](https://www.knime.com/blog/cohens-kappa-an-overview). 
    - This is the step nearly everyone skips, and skipping it makes judge
scores unfalsifiable.

Target finding: "hallucination rate rises as response fields are trimmed, judge
validated at kappa 0.8 against 30 hand labels."

## Token accounting

Decompose rather than measuring totals. A total dilutes the effect being
isolated.

| Component                                     | Varies with trimming?     |
| --------------------------------------------- | ------------------------- |
| System prompt                                 | No                        |
| **Tool schemas (JSON Schema for both tools)** | No                        |
| Tool result payloads                          | Yes, this is the variable |
| Model output                                  | Somewhat                  |

**The tool schema line deserves its own measurement.** MCP tool definitions sit in
context on every turn regardless of response shape. For two tools with nested
bbox arguments that is not nothing. If the schemas cost more than a typical
`crags_within` response, that is a finding the RFC did not anticipate and is
arguably more useful than the trimming result.

Sources:

- `usage` field on each API response: authoritative for what was actually billed.
- Anthropic `count_tokens` endpoint: measures a payload in isolation, which is
what half one needs.
- tiktoken as a second tokenizer. MCP is client agnostic and one vendor's numbers
are less credible than two.

**Gotcha: disable prompt caching**, or the accounting measures cache behaviour
rather than payload size.

For upstream API calls, instrument the Go server directly. A counter and a
structured log line per GraphQL request, keyed by tool. This is the API-usage half
of Stage 6 and the number Stage 8 (caching) depends on.

## Experimental design

**Independent variable:** response variant.
- raw GraphQL passthrough
- trimmed
- trimmed plus filtered
- paginated at several limits

**Dependent variables:** tokens consumed, task success, upstream call count.

**Question set:** 20 to 30 realistic queries with ground truth pulled from the
API. "Which crags near Squamish have more than 50 routes", "how many climbs at
the Chief", "what's at Smoke Bluffs". Plus the open-ended subset for groundedness
judging.

**Runs:** at least 5 iterations per (question, variant). Temperature 0 reduces
variance but does not eliminate it.

### Stats

**Pair the comparisons.** Every question is its own control across variants, so
use paired tests. Far more power than unpaired at this sample size. Be honest
that n=25 questions caps what can be claimed.

**Report tokens per *successful* task, not per task.** A variant that fails fast
looks cheap.

That framing points at the right visualization: plot each variant as a point in
(median tokens, success rate) space, look at the Pareto frontier. "Trimmed plus
filtered dominates raw on both axes" is a result. A table of averages is not.

**Two model sizes.** A finding that trimming hurts Haiku more than Opus is
plausible (smaller models handle noisy raw JSON worse) and would be genuinely
interesting to anyone shipping an MCP server.

### Method honesty, stated up front

- N is small.
- The thing being measured was built by the person measuring it.

Stated openly this is fine. Unstated it is the flaw a reviewer finds first.

## Tooling: LangGraph is not appropriate

LangGraph is built for stateful multi-actor applications with graph-structured
control flow: cycles, branching, human-in-the-loop, persistence across steps. This
harness is a loop over a config matrix that records numbers. No cycles, no
branching, no state worth persisting between runs.

Two active reasons against, beyond redundancy:

1. It puts an abstraction layer between the harness and the raw `usage` numbers,
which are the exact thing being measured.
2. Stage 2's thesis is that this project's code is defensible under questioning.
Wrapping the novel part in a framework undercuts that the same way the Strava
MCP does.

**Build it plain.** Roughly 200 lines: Python, the SDK, pandas, matplotlib. Python
rather than Go because that is where the stats and plotting live, and Stage 2 is
about the server, not the harness.

**Get this right:** the harness launches the actual compiled binary as a
subprocess and speaks MCP over stdio, the same as a real client. Not importing the
Go handlers, not mocking the tool layer. Otherwise it measures a reimplementation
rather than the thing that shipped.

### If the matrix outgrows a script

- **promptfoo**: config-driven matrix eval, custom graders, token tracking.
Precisely this shape.
- **Braintrust** or **Langfuse**: if hosted tracing becomes useful.

Worth naming in the writeup that these were considered and declined. "I evaluated
the tooling and it was overkill" reads better than not knowing it exists.

## Checklist

Half one, payload sizing:

- [ ]  Serialize responses across a bbox/density sweep
- [ ]  Token counts via `count_tokens` and tiktoken
- [ ]  Distribution, p99, stated breaking point
- [ ]  Measure tool schema cost separately

Half two, agent eval:

- [ ]  Question set with API-derived ground truth
- [ ]  Open-ended subset for groundedness
- [ ]  Harness: subprocess + stdio against the real binary
- [ ]  Disable prompt caching
- [ ]  5+ runs per (question, variant), two model sizes
- [ ]  Deterministic graders: exact match, set F1 with precision/recall split
- [ ]  Normalized name matching
- [ ]  Judge for groundedness, different model, binary/three-point, rubric plus
few-shot
- [ ]  Hand-label 30, report Cohen's kappa
- [ ]  Paired tests, tokens per successful task, Pareto plot

Instrumentation:

- [ ]  Counter and structured log per GraphQL request in the Go server, keyed by
tool