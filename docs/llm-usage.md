# CodeFlow LLM usage — v2 (Business Flow First)

이 문서는 LLM/코딩 에이전트가 CodeFlow v2 결과를 읽고 **비즈니스 흐름**으로 설명하기 위한 계약. CodeFlow는 코드 근거를 만들고 LLM은 사용자 언어로 설명한다 — `unknown`을 추론으로 메우지 않는다.

## 0. 설치 — LLM이 직접 실행 (one-shot)

```sh
curl -fsSL https://raw.githubusercontent.com/OWNER/codeflow/main/scripts/install.sh | bash
# 로컬 클론이 있으면
bash scripts/install.sh
# 확인
codeflow version
CODEFLOW_ADAPTER_DART_BIN="dartrun:$HOME/.codeflow-src/adapters/dart" codeflow doctor .
```

`$HOME/.local/bin`이 PATH에 없으면 `export PATH="$HOME/.local/bin:$PATH"`를 rc에 추가. Dart SDK 3.x 필요 — 없으면 `harvest/slice` 실패.

로컬 빌드 대체:
```sh
make build  # bin/codeflow 생성
```

## 1. 가장 짧은 사용 흐름

사용자 프롬프트 예: "이메일을 이용한 회원가입 흐름을 분석하고 flowview로 만들어줘"

에이전트는 CodeFlow 스킬(`skills/codeflow/SKILL.md`) 5단계를 따른다:

```
1. harvest_flows (query로 의도 매칭) → 2. 모호하면 질문 → 3. 없으면 analyze_flow → 4. get_flow_payload → 5. open_review
```

```sh
# MCP 없으면 CLI JSON으로 대체
CODEFLOW_ADAPTER_DART_BIN="dartrun:$PWD/adapters/dart" bin/codeflow flows --json ./testdata/example_app
CODEFLOW_ADAPTER_DART_BIN="dartrun:$PWD/adapters/dart" bin/codeflow publish ./testdata/example_app
bin/codeflow show flow-7232d63b96bd6efa --json | python3 -m json.tool
```

MCP가 있으면 기존 Core 재사용 — 매번 `init/serve` 불필요.

## 2. MCP 도구 선택 (v2 7종)

항상 정확한 `flowId` 또는 `entrySymbolPath`를 전달 — 기본값에 의존 금지.

| 목적 | 도구 | 입력 | 사용 시점 |
|---|---|---|---|
| 후보 탐색 | `harvest_flows` | `target?`, `query?` (예: "이메일 회원가입") | 모든 Business Flow 분석 전 — `intentSignals{derivedName, docLine, className}`으로 NL 매칭 |
| 단일 흐름 읽기 | `get_flow_payload` | `flowId` 또는 `entrySymbolPath` | 후보 1개 상세 조회 |
| 임의 진입점 분석 | `analyze_flow` | `entrySymbolPath` (예: `lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit`) | `harvest`에 없는 진입점 — 즉시 slice+fuse+게시 (기존 generation에 병합) |
| 세션 근거 제출 | `submit_flow_draft` | `artifact` (anchor 필수), `token?` | 에이전트가 여정 근거를 보강할 때 — `repoRelativePath+byteRange+fileHash+spanHash+enclosingSymbolPath+canonicalAstFingerprint` 전체 필요 |
| 단계 승인 | `approve_step` | `flowId`, `symbolPath`, `name`, `rules?`, `token?` | 사용자가 이름·규칙 승인 시 (E3, provenance=approved) |
| 미확정 확인 | `report_unknowns` | `flowId?` (없으면 전체) | 설명이 추론에 의존하는 즉시 |
| FlowView 열기 | `open_review` | `flowId?` | 사용자가 화면 확인 요청 시만 — MCP가 FlowView를 지연 기동하고 `?token=&flow=` 포함 URL 반환 |

`query` 예: `harvest_flows {"query":"이메일 회원가입"}` → `candidates[].intentSignals.derivedName="이메일을 회원가입한다"`와 부분일치.

`analyze_flow`는 파괴적이지 않음 — 기존 generation을 읽어 병합 후 게시.

## 3. 응답을 읽는 순서

FlowSpec envelope:
```
flowId → title → basisSha(64hex) → generatedAt → steps[] → unknowns[] + view URL
```
`steps[]` 각 원소:
```
ordinal → name → provenance(approved|session|derived|unknown) → freshness(fresh|stale|orphaned) → confidence → basisSha → anchor → rules/stateDelta/sideEffect/branch/codeLens
```

LLM 읽기 순서:
1. `basisSha`·`generatedAt`으로 어떤 스냅샷인지 확인.
2. `steps`를 `ordinal` 순으로 — `provenance` 권위 `approved>session>derived>unknown`, `freshness`가 `stale/orphaned`면 승인 큐로 안내.
3. `stateDelta {before→after}`, `sideEffect` (예: `SignupService.call`), `branch`로 인과 연결.
4. `codeLens {path,startLine,endLine}`로 코드 근거 제시 — 줄 번호를 만들지 말고 anchor 그대로 사용.
5. `unknowns[]`는 반드시 언급 — 비어 있어도 "확인되지 않은 부분 없음"으로 명시.
6. `open_review`의 `url`은 `?token=` 포함 — 그대로 제공.

## 4. 신뢰 상태 규칙

| 상태 | 말할 수 있는 것 | 금지 |
|---|---|---|
| `fresh + derived/session` | 코드 근거로 확인됐다고 설명 | 런타임에 반드시 실행됐다고 단정 |
| `stale` | 코드가 바뀌어 재승인 필요, `refresh`/`publish` 안내 | 이전 anchor를 현재 근거로 사용 |
| `orphaned` | 심볼이 사라짐, 승인 철회 필요 | 다른 심볼로 대체 |
| `unknown` | 어디까지 확인됐고 무엇이 빠졌는지 설명 | 그럴듯한 대상·상태를 선택 |

## 5. 사용자에게 설명하는 형식

```
비즈니스 여정 요약
이메일을 이용한 회원가입 — 사용자가 이메일을 제출해 가입을 완료하는 경로

현재 코드 흐름 (EmailSignupNotifier.submit)
1. 진행 상태로 갱신한다 [derived, fresh] — lib/features/auth/email_signup_notifier.dart:37
2. 외부 서비스에 작업을 요청한다: SignupService.call [derived, fresh] — :39
3. 성공 상태로 갱신한다 — :40
4. 오류 발생 시 예외를 포착한다 (분기) — :41
5. 실패 상태로 갱신한다 — :42-45

상태 변화
idle → submitting → done / failed(error: 'signup failed')

확인되지 않은 부분
없음 (stale 0, unknowns 0)

FlowView에서 코드 렌즈(5-20줄)와 함께 시각 검증 가능 — URL: http://127.0.0.1:4567/?token=...&flow=flow-7232d63b96bd6efa
```

원칙: **비즈니스 목적을 첫 문장에**, 원인 순서로, `unknowns`가 있으면 “왜 남았는지/다음에 확인할 코드”로.

## 6. 변경 후 재확인

코드 변경 시 이전 결과 재사용 금지 — `watch`가 500ms 폴링+mtime 필터로 감지하나 즉시 필요하면:
```sh
CODEFLOW_ADAPTER_DART_BIN="dartrun:$PWD/adapters/dart" bin/codeflow publish <repo>
bin/codeflow show <flowId> --json
```
`basisSha`가 바뀌었는지 확인 후 다시 설명.

## 7. 금지 사항

- CodeFlow와 별개 스캐너를 즉석 구현하지 않는다.
- product source를 CodeFlow 사용을 위해 수정하지 않는다.
- `unknown`을 `observed`로 승격하지 않는다.
- `token`, `.codeflow/pointer.json`, 비밀 값을 노출하지 않는다.
- 사용자가 요청하지 않았는데 FlowView를 열거나 외부에 게시하지 않는다.

## 8. LLM 최소 체크리스트

```
[ ] query로 harvest_flows 후 candidateId/flowId를 정확히 사용했는가?
[ ] freshness가 stale/orphaned면 승인 큐로 안내했는가?
[ ] steps를 ordinal 순, provenance 권위 순으로 읽었는가?
[ ] unknowns를 추측 없이 설명했는가?
[ ] 코드는 anchor/codeLens 그대로 인용했는가?
[ ] FlowView는 요청 시에만 open_review URL(token 포함)로 제공했는가?
```

로컬 실행·캐시 관리는 `docs/local-usage.md`, 전체 설계는 `docs/design-v2.md` 참조.
