# Golden Set

TODO:
- [ ] Fit to tools with Claude
- [ ] Enforce difficulty to numbers. Normalise to 5.5-12?

### Questions
1. Is "suitable for top roping" even possible to assess?
2. "short approach" would require distance from road calc
3. "easy logistics", "suitable for a group" -> vague

### Ideas

1. Use raster data to infer elevation?

### Overview

This directory contains the golden set for evaluating an LLM agent that interacts with the climbing MCP server.

The set is designed to evaluate two related capabilities:

Tool use — whether the agent selects and uses the MCP tools appropriately.
Recommendation quality — whether the agent correctly understands the climber's intent, constraints, experience, and preferences and produces an appropriate response.

The set intentionally contains both straightforward cases and adversarial cases where the correct behavior is to ask a question, relax a constraint, report no results, or handle a tool failure.

## File Format

Cases are stored as JSONL: one JSON object per line.

Each case has a stable id and follows this general structure:

```json
{
  "id": "routes_beginner_toprope_001",
  "category": "beginner",
  "subcategory": "top_rope",
  "user_query": "...",
  "context": {},
  "hard_constraints": [],
  "soft_constraints": [],
  "tool_expectations": {},
  "expected_behavior": {},
  "judge_dimensions": [],
  "difficulty": "medium",
  "tags": []
}
```

JSONL is preferred over one large JSON array because individual cases can be:

Added or removed without restructuring the file
Diffed easily in version control
Executed independently
Streamed into evaluation pipelines
Assigned stable IDs for regression tracking

### Case Fields
#### `id`

A unique, stable identifier for the case.

Use descriptive IDs rather than sequential numbers alone.

`routes_beginner_trad_firstlead_001`


Do not change an existing ID when the wording of a case is updated unless it represents a fundamentally different test.

#### `category`

The broad evaluation category.

Examples:

beginner
intermediate
advanced
constraint_handling
discovery
failure_handling
scope


This should describe the primary evaluation intent, not necessarily the user's climbing ability.

#### `subcategory`

A more specific description of what the case tests.

Examples:

first_sport_lead
first_trad_lead
multi_pitch_linkup
avoid_crowds
ambiguous_query
no_matching_routes
tool_error

#### `user_query`

The actual query presented to the agent.

This should be written as natural user language.

Do not turn this into a structured representation of the constraints.

Good:

I'm pretty new to climbing outside and want to do my first lead. Something easy, maybe 5.6 or 5.7, and preferably a single pitch.

Bad:

experience=beginner, style=sport, grade=5.6-5.7, pitches=1


The point is to test whether the agent can infer the relevant constraints from realistic language.

#### `context`

Information known to the evaluator about the scenario.

This can include information that is implicit in the query but useful for evaluation:
```json
{
  "climbing_experience": "new to outdoor climbing",
  "style": "sport",
  "grade_range": "5.5-5.7",
  "pitch_count": 1
}
```

Context should describe the scenario rather than prescribe the answer.

#### `hard_constraints`

Requirements that should materially affect whether a recommendation is considered correct.

Examples:
```json
[
  "single pitch",
  "sport climbing",
  "appropriate for a first outdoor lead"
]
```

Violating a hard constraint should normally result in a significant evaluation penalty.

#### `soft_constraints`

Preferences that should influence ranking but can reasonably be traded off.

For example:
```json
[
  "short approach",
  "low commitment",
  "5.6-5.7 preferred"
]
```

The distinction between hard and soft constraints is important.

An agent should not fail merely because it recommends a slightly less ideal route when no route satisfies every preference.

#### `tool_expectations`

Describes expected MCP behavior.

Example:
```json
{
  "required": ["SearchRoutes"],
  "preferred": ["GetTicks"],
  "optional": [],
  "forbidden": []
}
```


The categories have the following meanings:

required — the task normally cannot be completed correctly without this tool.
preferred — using this tool is the expected/high-quality strategy, but another valid strategy may exist.
optional — useful but not necessary.
forbidden — using this tool would indicate inappropriate behavior.

Avoid requiring an exact tool-call sequence unless there is a strong reason to do so.

The goal is to evaluate appropriate tool use, not whether the agent happened to reproduce one particular implementation.

#### `parameter_assertions`

Use these when the correctness of tool parameters is itself important.

Example:

```json
{
  "parameter_assertions": [
    {
      "tool": "SearchRoutes",
      "assert": {
        "style": "sport",
        "pitches": 1,
        "max_grade": "5.7"
      }
    }
  ]
}
```

Assertions should focus on meaningful semantic requirements rather than incidental implementation details.

#### `expected_behavior`

This describes what a high-quality agent should do.

Use three levels:

must — essential behavior.
should — desirable behavior.
must_not — important failure modes.

Example:
```
{
  "must": [
    "Recognize that the user is new to outdoor climbing",
    "Prioritize suitability for a first outdoor lead"
  ],
  "should": [
    "Consider protection and route-finding"
  ],
  "must_not": [
    "Assume that any 5.7 is automatically suitable"
  ]
}
```

This is intentionally more semantic than an exact expected answer.

Multiple routes may be valid recommendations, so the golden set should generally evaluate properties of the answer, not require a particular route name.

#### `judge_dimensions`

The dimensions the LLM judge should evaluate.

Examples:
```
[
  "constraint_following",
  "tool_selection",
  "recommendation_quality",
  "explanation"
]
```

Useful dimensions include:
```
constraint_following
constraint_prioritization
tool_selection
tool_parameter_accuracy
getticks_usage
recommendation_quality
ranking
clarification
ambiguity_handling
multi_step_reasoning
party_awareness
scope_awareness
honesty
failure_handling
hallucination_resistance
```

Not every case needs every dimension.

#### `difficulty`

Suggested values:

easy
medium
hard


Difficulty should describe the agent task, not the climbing difficulty.

For example, a 5.5 first-lead query can be a hard evaluation case if correctly answering it requires reasoning about protection and first-lead suitability.

tags

Tags provide a flexible way to slice evaluation results.

Examples:
```json
[
  "beginner",
  "first_lead",
  "get_ticks",
  "multi_step"
]
```

Prefer multiple reusable tags over creating an increasingly deep category hierarchy.

## Evaluation Philosophy
### Don't Require a Single Correct Route

Climbing recommendations are often non-deterministic.

If several routes satisfy the user's requirements, all of them can be valid.

The judge should therefore ask:

Did the agent produce a recommendation that satisfies the user's important requirements?

rather than:

Did the agent return the exact route stored in the golden set?

The golden set should specify expected properties and behavior, not necessarily an exact answer.

Hard Constraints Matter More Than Preferences

For example:

"I need a single-pitch route."

should outweigh:

"I'd prefer something popular."

A route that violates the pitch requirement should not beat a slightly less popular route that satisfies it.

### Grade Is Not the Whole Recommendation

The cases deliberately test situations where climbing grade alone is insufficient.

For example, a first trad lead should consider factors such as:

Protection
Gear-placement opportunities
Route-finding
Exposure
Pitch count
Approach
Descent
Overall commitment

The exact dimensions depend on what metadata the MCP exposes.

User Experience and Area Familiarity Are Independent

An important distinction throughout the set is:

new to climbing != new to the area


For example:

"I'm new to the area but climb 5.11+."

means the agent should recommend hard climbing while accounting for the user's lack of local knowledge.

Similarly:

"I've climbed all the obvious classics."

means the agent should avoid repeatedly returning the area's standard recommendations.

### Clarification Is Sometimes the Correct Answer

Not every query should result in a tool call.

For example:

"Where should I climb tomorrow?"

does not contain enough information to produce a useful recommendation.

The agent should ask for relevant information rather than making arbitrary assumptions.

These cases are important because otherwise an agent can appear highly capable simply by confidently searching and returning something for every query.

## GetTicks

GetTicks should be treated primarily as a ranking/popularity signal, rather than as a generic mandatory step for every route search.

Good use cases include:

"I want to avoid crowds."
"What are the most climbed routes?"
"Show me lesser-climbed areas."
"Give me classics that aren't the usual crowded recommendations."

It should generally not be required simply because a route search is being performed.

When GetTicks is used in a case, the golden set should specify why it is relevant.

For example:

SearchRoutes → find eligible routes
GetTicks → estimate popularity
Rank → balance quality and popularity


This makes the tool's role explicit and gives the judge something meaningful to evaluate.

## Failure Cases

The golden set should contain cases where the correct behavior is not a successful recommendation.

Important failure modes include:

### No Results

The agent should say that no exact match was found and, where useful, offer alternatives by relaxing constraints.

It should not invent a matching route.

### Tool Failure

If an MCP tool fails or times out, the agent should not fabricate tool results.

Depending on the available tools, it may:

Retry
Use another appropriate tool
Explain the limitation

### Missing Metadata

If the MCP does not return information necessary to verify a constraint, the agent should distinguish:

"The route has a short approach."

from:

"I don't have enough data to verify the approach length."

### Unsupported Requests

If the MCP only provides route information, it should not pretend that route metadata answers unrelated questions such as equipment recommendations.

## Recommended Test Coverage

The set should be maintained as a coverage matrix, rather than simply growing organically.

Important dimensions include:

Dimension	Example values
Experience	beginner / intermediate / advanced
Area familiarity	newcomer / familiar / local
Style	sport / trad / top rope
Grade	5.5–5.6 / 5.7–5.9 / 5.10 / 5.11+
Pitch	single / multi
Approach	short / moderate / long
Popularity	irrelevant / avoid crowds / most popular
Objective	first lead / project / classic / adventure
Party	individual / partner / group / mixed ability
Query specificity	vague / moderate / precise
Constraints	compatible / conflicting / impossible
Tool usage	single tool / multi-tool / GetTicks
Result state	results / no results / partial data / tool failure

New cases should be added deliberately to cover gaps in this matrix.

For example, useful combinations include:

Beginner × trad × first lead
Beginner × group × top rope
Advanced × newcomer × hard classics
Intermediate × local × obscure × GetTicks
Intermediate × multi-pitch × half-day
Mixed ability × group
Conflicting constraints × no results

## Adding a New Case

When adding a case:

Give it a unique, descriptive ID.
Write the user_query as realistic natural language.
Record inferred information in context.
Separate hard_constraints from soft_constraints.
Specify expected tool behavior.
Describe semantic expected behavior rather than one exact answer.
Identify the relevant judge dimensions.
Add reusable tags.
Check whether the case fills a coverage gap.
Prefer cases that expose a specific failure mode over redundant variations of existing cases.
What Makes a Strong Golden Case?

A strong case has a clear evaluation question.

For example:

Does the agent use popularity data when the user explicitly wants to avoid crowds?

is better than:

Find a good climbing route.

Likewise:

Does the agent recognize that "new to the area" does not mean "beginner"?

is a strong targeted test.

The goal is for every case to tell us something specific about whether the MCP agent is behaving correctly.

## Future Additions

The next cases worth adding are likely to be:

Weather/access-dependent recommendations
Routes requiring interpretation of multiple pieces of route metadata
Multi-tool planning tasks
Cases where two constraints must be traded off
Cases where the user changes one constraint in a follow-up turn
Follow-ups referring to a previous recommendation ("What about something harder?")
Cases where the user explicitly rejects the agent's first recommendation
Duplicate or near-duplicate route results
Contradictory metadata returned by tools
Tool calls with incorrect parameters
Cases where the agent should stop searching after finding sufficient results
Cases where additional tool calls materially improve ranking
Cases where additional tool calls add no value

The most valuable evolution of the set will be from "does the agent find a good route?" toward "does the agent correctly reason about intent, tools, constraints, uncertainty, and tradeoffs?".