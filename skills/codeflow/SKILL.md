# CodeFlow Skill — Natural Language Prompt → FlowView

AI 에이전트가 사용자의 한국어 자연어 프롬프트(예: "이메일을 이용한 회원가입 … flowview로 만들어줘")를 CodeFlow MCP 도구로 정확히 수행하는 절차.

## Role Split (LLM vs CORE)
- **LLM(Agent)이 담당:** 한국어 의도 해석, 후보 매칭, 모호성 질문, 앵커 포함 draft 작성.
- **CORE가 담당:** 결정적 스코어링·슬라이싱·융합·게시, `intentSignals{derivedName, docLine, className}` 제공. CORE는 LLM을 호출하지 않는다.

## 5-Step Workflow

### 1. harvest_flows
```json
{"name":"harvest_flows","arguments":{"target":".", "query":"이메일 회원가입"}}
```
`query`는 선택 — 한국어 토큰을 공백 분리해 `derivedName`/`docLine`/`className` 부분일치 필터. 결과 `candidates[].intentSignals.derivedName` (예: `이메일을 회원가입한다`)을 프롬프트와 대조.

### 2. Clarify (모호성 해소)
`candidates`가 2개 이상 동일 점수면 사용자에게 질문:
> "회원가입 후보 2개: EmailSignupNotifier.submit / EmailSignupScreen.onSignupPressed — 어느 진입점을 분석할까요?"

### 3. analyze_flow — Fallback
마커가 놓친 임의 진입점은 `harvest`에 없으므로 직접 지정:
```json
{"name":"analyze_flow","arguments":{"entrySymbolPath":"lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit"}}
```
단일 `FlowSpec`을 즉시 게시 (기존 generation에 병합, 덮어쓰지 않음).

### 4. get_flow_payload + submit_flow_draft
```json
{"name":"get_flow_payload","arguments":{"flowId":"flow-..."}}
{"name":"submit_flow_draft","arguments":{"artifact":{...},"token":"<per-run>"}}
```
`artifact.journeyDraft.steps[].anchor`는 필수 — `repoRelativePath+byteRange+fileHash+spanHash+enclosingSymbolPath+canonicalAstFingerprint` 전체. `rationale`은 단일 `Rules` 원소로 저장됨. 스키마 `session-artifact` 검증 실패 시 재작성.

### 5. open_review
```json
{"name":"open_review","arguments":{"flowId":"flow-..."}}
```
MCP가 FlowView를 지연 기동(`127.0.0.1:4567`, 실패 시 `:0`)하고 `?token=&flow=` 포함 URL 반환. 브라우저에서 즉시 확인.

## Anchor Rules
- 앵커 없이 제출 금지. 라인 번호만으로 앵커 금지.
- 재제출 시 `checkFreshness`가 `fresh|stale|orphaned`를 판정, `stale`은 승인 큐로 이동.

## Tool Reference (7)
`harvest_flows` (query?) | `get_flow_payload` | `analyze_flow` | `submit_flow_draft` (token) | `approve_step` (token) | `report_unknowns` | `open_review`
