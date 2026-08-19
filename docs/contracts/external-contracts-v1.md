# External contracts v1

`codeflow.external-contracts.json` is optional, repository-local evidence for
a supported external call. CodeFlow never executes the remote API.

```json
{
  "version": "1",
  "external": {
    "PaymentsApi.charge": {"result": "charge accepted"}
  }
}
```

The key must exactly match the source receiver and method name. A string value
is also accepted in place of the result object. Missing, malformed, or
non-matching contracts produce `EXTERNAL_BOUNDARY_UNKNOWN` at the call site.
