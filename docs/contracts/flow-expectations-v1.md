# Flow expectations v1

`codeflow.flow-expectations.json` is an optional repository-owned guardrail for
important flows. It never creates facts and never changes `unknown` into
`observed`; `codeflow verify` first compiles current source evidence and then
checks that document against the contract.

By default the contract lives in the inspected repository. For local review
without changing that repository, pass an explicit file with
`--expectations /path/to/expectations.json`. Its machine schema is
[`flow-expectations.v1.schema.json`](./flow-expectations.v1.schema.json).

```json
{
  "version": "1",
  "flows": {
    "route:/join": {
      "required_results": ["route:/home", "route:/auth"],
      "required_causal_kinds": ["changes_state", "observed_by", "produces"],
      "allowed_debt_reasons": [],
      "max_open_debt": 0
    }
  }
}
```

Verification fails when a required observed result or causal relation
disappears, an unapproved debt reason appears, or the open-debt budget is
exceeded. The contract must not contain secrets or runtime/session claims.
