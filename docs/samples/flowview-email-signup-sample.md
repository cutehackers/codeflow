# FlowView 샘플 — "이메일을 이용한 회원가입" 프롬프트

프롬프트:
```
이메일을 이용한 회원가입 기능에 대한 흐름을 분석하고 flowview로 만들어줘
```

## 1. CodeFlow가 선택하는 후보

`harvest_flows {"query":"이메일 회원가입"}` → `intentSignals.derivedName="이메일을 회원가입한다"` 부분일치로 1위:

```
candidateId: cand-7232d63b96bd6efa (flow-7232d63b96bd6efa)
entrySymbolPath: lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit
triggerClass: user_action / markerKind: notifier_method / score 0.735
```

`analyze_flow` 없이 `publish`된 10개 중 해당 flow를 `open_review`로 오픈. `query`가 없으면 후보 10개 중 `Submit`을 Agent가 직접 선택 — 스킬(`skills/codeflow/SKILL.md` 5단계)이 이 매칭을 안내.

## 2. FlowView에서 보이는 화면 — 흑백 모노크롬 웹페이지 (레거시 스타일 유지)

> 레거시 `docs/samples/flowview-legacy-email-signup.html`의 흑백 스타일(`--ink:#111, --paper:#fff, --line:#bdbdbd`, 세로 타임라인·Hero Code Lens·Architecture Map)을 그대로 유지. `internal/flowview/embedded_html.go`는 다크 테마가 아닌 **흑백 모노크롬**으로 복구됨.

실 게시 결과 기반

> 실제 `testdata/example_app`에서 `CODEFLOW_ADAPTER_DART_BIN="dartrun:$PWD/adapters/dart" bin/codeflow publish` 후
> `flow-7232d63b96bd6efa.json`을 렌더한 결과. `bin/codeflow view`로 여는 화면과 동일.

```
┌─────────────────────────────────────────────────────────────────┐
│ [회원가입: Submit]  ● fresh  basis e701a70a…  5 steps  unknowns 0  │
├─────────────────────────────────────────────────────────────────┤
│ ① 진행 상태로 갱신한다                  [derived · fresh · 0.85]  │
│    state: idle → state.copyWith(status: EmailSignupStatus.submitting)│
│    📍 lib/features/auth/email_signup_notifier.dart:37  [▸ 코드 37]│
│                                                                 │
│ ② 외부 서비스/저장소에 작업을 요청한다: SignupService.call       │
│    [derived · fresh]  sideEffect: SignupService.call             │
│    📍 lib/features/auth/email_signup_notifier.dart:39  [▸ 코드 39]│
│    → boundary_call  depth 0                                      │
│                                                                 │
│ ③ 성공 상태로 갱신한다                  [derived · fresh]        │
│    state: idle → state.copyWith(status: EmailSignupStatus.done)  │
│    📍 lib/features/auth/email_signup_notifier.dart:40            │
│                                                                 │
│ ④ 오류 발생 시 예외를 포착하고 대체 처리한다 [derived · fresh]  │
│    branch: 분기 처리  on Exception                               │
│    📍 lib/features/auth/email_signup_notifier.dart:41            │
│                                                                 │
│ ⑤ 실패 상태로 갱신한다                  [derived · fresh]        │
│    state: idle → state.copyWith(status: failed, error: 'signup failed')│
│    📍 lib/features/auth/email_signup_notifier.dart:42-45         │
│                                                                 │
│  확인되지 않은 부분: 없음 (unknowns [])                          │
│  Cmd+K → 다른 후보( PlaceOrderUseCase.call, CartBloc._onItemAdded 등) 검색 가능 │
└─────────────────────────────────────────────────────────────────┘
```

배지: `derived` 회색 점선, `fresh` 실선. `stale/orphaned`면 노란 큐 배너로 이동 — 현재 0개.

코드 렌즈: 각 단계 `▸ 코드` 클릭 시 해당 줄 5-20줄 인라인 전개, 우측 `permalink`는 remote가 있을 때만 표시.

## 3. 원본 FlowSpec JSON (발췌 — `flow-7232d63b96bd6efa.json`)

```json
{
  "flowId": "flow-7232d63b96bd6efa",
  "title": "Submit",
  "basisSha": "e701a70a35db50e4bccb2c0e012d9737a6e78b631cc775b7bcc9ee79d0cd935f",
  "generatedAt": "2026-08-24T09:09:24Z",
  "steps": [
    {
      "ordinal": 1,
      "name": "진행 상태로 갱신한다",
      "provenance": "derived",
      "freshness": "fresh",
      "confidence": 0.85,
      "basisSha": "e701a70a...",
      "anchor": {
        "repoRelativePath": "lib/features/auth/email_signup_notifier.dart",
        "byteRange": [1028, 1089],
        "fileHash": "ecd9f663...",
        "spanHash": "8a8713fb...",
        "enclosingSymbolPath": "EmailSignupNotifier.submit",
        "canonicalAstFingerprint": "8a8713fb..."
      },
      "codeLens": { "path": "lib/features/auth/email_signup_notifier.dart", "startLine": 37, "endLine": 37 },
      "stateDelta": { "before": "status: idle", "after": "state.copyWith(status: EmailSignupStatus.submitting)" }
    },
    {
      "ordinal": 2,
      "name": "외부 서비스/저장소에 작업을 요청한다: SignupService.call",
      "provenance": "derived",
      "freshness": "fresh",
      "sideEffect": "SignupService.call",
      "codeLens": { "path": "lib/features/auth/email_signup_notifier.dart", "startLine": 39, "endLine": 39 }
    }
  ],
  "unknowns": []
}
```
전체 5 step, `unknowns: []`, `basisSha`는 `pointer.json`과 동일.

## 4. 로컬에서 직접 확인

```bash
# 설치 (one-shot)
bash scripts/install.sh
# 또는
make build  # bin/codeflow

# 게시 및 조회
CODEFLOW_ADAPTER_DART_BIN="dartrun:$PWD/adapters/dart" bin/codeflow publish ./testdata/example_app
CODEFLOW_ADAPTER_DART_BIN="dartrun:$PWD/adapters/dart" bin/codeflow show flow-7232d63b96bd6efa
# JSON 원문
CODEFLOW_ADAPTER_DART_BIN="dartrun:$PWD/adapters/dart" bin/codeflow show flow-7232d63b96bd6efa --json | python3 -m json.tool

# FlowView 열기
bin/codeflow view ./testdata/example_app
# → 터미널에 출력된 http://127.0.0.1:4567/?token=...&flow=flow-7232d63b96bd6efa 로 접속
# Cmd+K로 다른 흐름 검색

# MCP로 프롬프트 재현
# harvest_flows {"query":"이메일 회원가입"} → analyze_flow {"entrySymbolPath":"lib/features/auth/email_signup_notifier.dart#EmailSignupNotifier.submit"} → open_review {"flowId":"flow-7232d63b96bd6efa"}
```

위 화면과 `show --json` 출력이 일치하면 설치·게시·렌더가 모두 정상.
