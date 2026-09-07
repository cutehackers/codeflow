# VS-10 real-environment release evidence execution

This guide produces the evidence consumed by `EvaluateReleaseCapability`. Repository fixtures under `schemas/fixtures/rflsc.*.v2` and `internal/testfixture` test the evaluator only. They are synthetic and must not be cited as production benchmark evidence.

## 반복 성능 측정용 `flowmeter`

CodeFlow 저장소에서 빌드한 뒤 실행합니다.

```sh
make build-flowmeter
./bin/flowmeter run
```

`flowmeter`가 내부의 고정된 `live-semantic-compiler-v1` benchmark project를 직접 실행합니다. 외부 collector나 별도 타겟 프로젝트는 필요하지 않습니다. 현재 실행 파일의 digest, 고정 corpus ref, OS, CPU, Go 버전, 24개 trace와 측정값은 `.codeflow/flowmeter/runs/<execution-id>/benchmark-result.json`에 저장됩니다.

성능을 개선한 뒤 다시 `flowmeter run`을 실행하고 비교합니다.

```sh
./bin/flowmeter compare
```

비교는 corpus ref와 실행 환경이 정확히 같은 최근 두 결과에만 허용됩니다. 다른 corpus나 환경의 수치를 섞지 않습니다.

이 기본 루틴은 live semantic compiler의 activity latency, current-or-gap latency, 전체 실행 시간, Go runtime system memory를 측정합니다. 이것만으로 VS-10 전체 release 승인을 뜻하지는 않습니다. 아래의 품질 지표, 12개 복구 시나리오, child-slice 실행 근거까지 평가하려면 고급 `prepare`, `run --dir`, `approve`, `finalize`, `compare latest --root` 절차를 사용합니다.

## 1. Declare the evaluation before running it

Create a `rflsc.release-profile.v2` document with the exact `targetVersion` and the release target's real OS, architecture, CPU, logical CPU count, memory, repository shape, file count, byte count, languages, browser, active scope, load profile, and exact toolchain versions. Record immutable refs for the repository fixture and toolchain artifacts. The evaluation `targetVersion` must equal the sealed profile value.

Create a versioned `rflsc.scenario-manifest.v2` corpus bound to that profile by both `profileId` and the exact sealed `profileRef`. It must include all of these scenario kinds:

1. `rapid_edit`
2. `multi_file`
3. `rename_delete`
4. `syntax_error`
5. `branch_switch`
6. `watcher_gap`
7. `open_closure`
8. `late_result`
9. `cas_conflict`
10. `adapter_crash`
11. `model_crash`
12. `reconnect`

Each scenario needs a stable scenario ID, affected capability IDs, and an immutable fixture ref. Every capability ID must exist in the profile. Do not modify the corpus after execution. Publish a new corpus version instead.

## 2. Execute and capture immutable observations

Run the complete corpus in the declared environment. Every execution report must contain the exact sealed `profileRef` and `corpusRef`, in addition to their logical IDs and corpus version. Record the exact command, execution timestamp, scenario ID, end-to-end trace ID, trace artifact ref, toolchain ID, affected capabilities, result, failure reason, and recovery condition. A result's capabilities must be a subset of its corpus scenario, and its toolchain must appear in both the profile and execution report. A passing result must not contain failure or recovery data. A trace ID must retain one identity across all reports.

For every trace population, record two independent end-to-end latency observations:

- `activity_latency_ms`: input event to observable activity-state update.
- `current_or_gap_latency_ms`: the same input event to current proof or explicit verified gap.

The two metric series must contain the same trace identities. Do not sum stage P95 values and do not reuse one latency sample as both distributions.

Record these measures separately:

- Quality: `precision`, `recall`, `semantic_delta`, `alignment_validity`, `unknown_coverage`, `comprehension`.
- Resource: `peak_memory_bytes`.
- Latency: `activity_latency_ms`, `current_or_gap_latency_ms`.

Every metric series must name its capability. Every sample must bind the profile ID, scenario ID, trace ID, trace ref, toolchain ID, and immutable evidence ref to a result for that capability. The evaluator requires all nine metrics for each declared capability and computes each gate only from that capability's samples. One capability cannot borrow another capability's scenario or metric evidence. A capability, metric, profile, scenario, trace, trace ref, and toolchain tuple may appear only once; repeat runs need new trace IDs. Category and unit cannot change when reports are merged. Latency and resource observations cannot be negative, and quality ratios must be within 0–1.

For every affected capability, execute and record one check for each hard invariant: proof-less current, false settlement, cross-generation mix, fabricated evidence, fabricated runtime, unsafe approval, secret leak, path leak, race, and unrecovered event gap. Every check needs a pass/fail result bound to a scenario trace and immutable evidence ref. A missing check makes the evaluation incomplete; a failed check blocks the capability with its failure reason and recovery condition. Passing invariant evidence remains attached to the GA capability. Do not convert failures into numeric penalties.

For comprehension, retain the answer corpus, expected answers, observed answers, scoring method, elapsed times, and unknown-handling results as immutable artifacts. The release evaluation consumes the resulting separate `comprehension` series. It does not generate that evidence.

## 3. Attach executed child-slice evidence

The full-product profile declares capability ID `full-product-release` and attaches all active R2 child slices `VS-01` through `VS-09`. That dependency set must be exact. The evaluator requires these acceptance ranges: VS01 A1–A10, VS02 A1–A11, VS03 A1–A15, VS04 A1–A14, VS05 A1–A7, VS06 A1–A9, VS07 A1–A7, VS08 A1–A10, and VS09 A1–A13.

A narrower capability may declare only its owning child subset. A failure in an unreferenced child does not block that scoped capability. Each referenced child record must contain:

- the slice and approved contract refs;
- the exact release `targetVersion` and the profile repository's immutable fixture ref as `sourceRef`;
- the implementation package/ref and executed test-binary digest;
- one observed acceptance record per required acceptance criterion, with exact test ID, package, run count, pass count, result, and evidence ref;
- the evaluator-owned per-slice required check IDs below, each with its exact executed command, observed result, and evidence ref.

| Slice | Required check IDs |
|---|---|
| VS-01 | `registry`, `acceptance`, `protocol`, `static`, `build`, `regression`, `security`, `migration`, `concurrency` |
| VS-02 | `registry`, `acceptance`, `dart`, `typescript`, `go`, `static`, `build`, `security`, `concurrency_reliability`, `regression` |
| VS-03 | `registry`, `acceptance`, `contract`, `static`, `build`, `concurrency`, `reliability`, `browser`, `a11y`, `regression` |
| VS-04 | `registry`, `acceptance`, `contract`, `static`, `build`, `regression`, `browser`, `a11y`, `security` |
| VS-05 | `registry`, `acceptance`, `full_slice`, `static`, `build`, `security`, `regression` |
| VS-06 | `registry`, `acceptance`, `full_slice`, `static`, `build`, `security`, `concurrency_reliability`, `regression` |
| VS-07 | `registry`, `acceptance`, `full_slice`, `static`, `build`, `security`, `browser`, `a11y`, `regression` |
| VS-08 | `registry`, `acceptance`, `full_slice`, `static`, `build`, `security`, `concurrency_reliability`, `browser`, `a11y`, `regression` |
| VS-09 | `registry`, `acceptance`, `full_slice`, `static`, `build`, `security`, `migration_persistence`, `concurrency_reliability`, `browser`, `a11y`, `regression` |

Missing, duplicate, or unexpected acceptance/check identities make the evidence incomplete.

A document label, unchecked Boolean, zero matched tests, missing command, or absent child record is incomplete evidence.

Execute each active child contract's `Required=Yes` Verification Plan row exactly as written and save stdout, stderr, exit status, command, binary digest, and timestamp in the referenced evidence artifact. The check IDs in the table label those real commands; the synthetic fixture commands are not substitutes.

## 4. Approve thresholds as evidence

Create an `ApprovedThresholdSet` only after reviewing the real measurements. Each threshold must name one capability, one metric, operator, value, unit, and its own approval/decision ref. Bind the set with `profileId`, exact `profileRef`, `corpusId`, `corpusVersion`, and exact `corpusRef`. One set-level label cannot approve every numeric threshold.

The 300 ms activity target cites the parent/Raw §16 policy. The 3,000 ms current-or-gap target cites decision D9. Decision D36 governs evidence completeness only and is rejected as numeric threshold approval. Precision, recall, semantic delta, alignment validity, unknown coverage, comprehension, and resource limits require a new user-approved numeric decision before production evaluation. Any changed value requires a new per-metric approval ref and a new immutable threshold artifact.

Record those new approvals in a separate trusted `ApprovedThresholdDecisionSet` JSON file loaded when the server starts. Each decision record must exactly match `decisionRef`, `metric`, `capabilityId`, `profileId`, `profileRef`, `corpusId`, `corpusVersion`, `corpusRef`, `operator`, `value`, and `unit`. Seal the file with the same top-level `artifactRef` process in section 5. The evaluator's built-in records cover only Raw §16 activity latency at `lte 300 ms` and D9 current-or-gap latency at `lte 3000 ms`. Unknown, resealed-artifact, or scope-mismatched refs stay incomplete. The request body cannot approve its own decisions.

Example structure, with values and refs replaced only by actual user-approved decisions:

```json
{
  "artifactRef": "sha256:<digest after canonical sealing>",
  "decisions": [
    {
      "decisionRef": "decision:<approved-quality-decision>",
      "metric": "precision",
      "capabilityId": "full-product-release",
      "profileId": "<profile-id>",
      "profileRef": "sha256:<exact-profile-digest>",
      "corpusId": "<corpus-id>",
      "corpusVersion": "<corpus-version>",
      "corpusRef": "sha256:<exact-corpus-digest>",
      "operator": "gte",
      "value": "REPLACE_WITH_APPROVED_NUMBER",
      "unit": "ratio"
    }
  ]
}
```

The placeholder is intentionally invalid until replaced with the exact user-approved numeric value. It is not a threshold recommendation.

## 5. Seal top-level artifacts

For the profile, corpus, every execution report, threshold set, child evidence document, and approved decision set:

1. Remove only the top-level `artifactRef` field.
2. Parse and re-encode the JSON object with Go `encoding/json` canonical key ordering.
3. Compute SHA-256 over those bytes.
4. Set `artifactRef` to `sha256:<lowercase hex digest>`.
5. Store the exact sealed bytes in the trusted evidence store.

Changing any top-level content without resealing makes the evaluation incomplete. Leaf trace and fixture refs may remain opaque content-addressed refs, but they must resolve in the measurement environment's evidence store.

This is a non-cryptographic local-maintainer boundary. Content hashing detects mutation and binds scope; it does not establish a remote signer's identity. Keep the sealed bytes in the locally trusted evidence store and do not expose this endpoint as a remote attestation service.

The following helper prints the evaluator-compatible ref for one JSON document. Run it from the repository root and set the returned value as that document's top-level `artifactRef`:

```sh
seal_helper_dir="$(mktemp -d)"
trap 'rm -rf "$seal_helper_dir"' EXIT
cat > "$seal_helper_dir/main.go" <<'GO'
package main

import (
  "crypto/sha256"
  "encoding/hex"
  "encoding/json"
  "fmt"
  "os"
)

func main() {
  data, err := os.ReadFile(os.Args[1])
  if err != nil { panic(err) }
  var document map[string]any
  if err := json.Unmarshal(data, &document); err != nil { panic(err) }
  delete(document, "artifactRef")
  canonical, err := json.Marshal(document)
  if err != nil { panic(err) }
  digest := sha256.Sum256(canonical)
  fmt.Println("sha256:" + hex.EncodeToString(digest[:]))
}
GO
artifact_path="evidence/release-profile.json"
artifact_ref="$(go run "$seal_helper_dir/main.go" "$artifact_path")"
jq --arg ref "$artifact_ref" '.artifactRef = $ref' "$artifact_path" > "$artifact_path.tmp"
mv "$artifact_path.tmp" "$artifact_path"
```

Seal in dependency order: profile, corpus, reports, threshold set, child evidence, then approved decision set. Copy the exact upstream refs into dependent documents before sealing. Repeat the last four commands for each artifact, then rerun the helper and confirm its output equals the stored `artifactRef` before evaluation.

## 6. Evaluate through a public surface

Submit one `ReleaseEvaluationInput` containing `targetVersion`, `evaluationId`, `evaluatedAt`, `profile`, `corpus`, `reports`, `thresholds`, and `childEvidence`.

- REST: `POST /api/release/capability` with the input as the JSON body.
- MCP: call `validate_release_capability` with the input under `evaluation`.

Start FlowView in one terminal with a token read without echoing it:

```sh
read -r -s -p 'FlowView token: ' codeflow_server_token
printf '\n'
go run ./cmd/codeflow serve --port 8080 --token "$codeflow_server_token" --release-decisions evidence/approved-threshold-decisions.json .
```

Then assemble and submit the evaluation from another terminal:

```sh
read -r -s -p 'FlowView token: ' codeflow_client_token
printf '\n'
jq -s '.' evidence/execution-report-*.json > evidence/execution-reports.json
jq -s '.' evidence/child-vs-*.json > evidence/child-evidence.json
jq -n \
  --slurpfile profile evidence/release-profile.json \
  --slurpfile corpus evidence/scenario-manifest.json \
  --slurpfile reports evidence/execution-reports.json \
  --slurpfile thresholds evidence/approved-thresholds.json \
  --slurpfile children evidence/child-evidence.json \
  --arg targetVersion "vX.Y.Z" \
  --arg evaluationId "release-vX.Y.Z-real-environment" \
  --arg evaluatedAt "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{targetVersion:$targetVersion,evaluationId:$evaluationId,evaluatedAt:$evaluatedAt,profile:$profile[0],corpus:$corpus[0],reports:$reports[0],thresholds:$thresholds[0],childEvidence:$children[0]}' \
  > evidence/release-evaluation-input.json
curl --fail-with-body \
  -H 'content-type: application/json' \
  -H "X-CodeFlow-Token: $codeflow_client_token" \
  --data-binary @evidence/release-evaluation-input.json \
  http://127.0.0.1:8080/api/release/capability \
  | tee evidence/release-evaluation-output.json
unset codeflow_client_token
```

Enter the same token in both terminals. Do not place the literal token in shell history, logs, evidence artifacts, or committed files.

Missing evidence returns a successful transport response with `benchmarkReport.status = "incomplete"`, empty metrics/gates, and `releaseReady = false`. Invalid JSON remains a request error. A release is ready only when all scoped gates pass, all child executions pass, no hard invariant blocks a capability, and every declared capability is evidence-derived `ga` for the exact profile, corpus version, scenarios, and toolchains.

For MCP, start `codeflow mcp --release-decisions evidence/approved-threshold-decisions.json .`. Omitting `--release-decisions` is safe and deterministic: only the exact Raw §16 and D9 built-ins are recognized, so profiles that contain quality or resource gates remain incomplete.

Archive the input bytes, returned benchmark report, returned capability matrix, and public-call response digest together. Treat any synthetic fixture output as evaluator test evidence, never as a release benchmark result.
