# FlowView 정적 에셋 분리 및 Go embed.FS 서빙 현대화 스펙

- Contract ID: `FLOWVIEW-EMBEDDED-ASSETS-REFACTORING`
- Contract Status: Approved
- Created: 2026-09-23
- Intent Status: Hardened
- Source: 사용자 지시 (`internal/presenter/flowview/svelte_flow_view_html.go`, `internal/presenter/flowview/svelte_flow_view.html`의 인라인 임베드 스크립트 관리 및 유지보수성 개선 요청)
- Decision Records: [`docs/design/decisions/2026-09-23-flowview-embedded-assets-refactoring-ko.md`](../decisions/2026-09-23-flowview-embedded-assets-refactoring-ko.md)
- Glossary: [`docs/design/glossary.md`](../glossary.md)

---

## 1. Problem and Goal

현재 FlowView 프론트엔드는 `vite-plugin-singlefile`을 사용하여 모든 Svelte 5 런타임, 컴포넌트 JS, CSS를 138KB가 넘는 단일 라인 minified 문자열로 인라인화하여 `internal/presenter/flowview/svelte_flow_view.html`에 저장하고, Go에서 `//go:embed svelte_flow_view.html`로 단일 문자열로 서빙하고 있다.

이 구조는 다음과 같은 심각한 관리 및 유지보수 문제를 야기한다:
1. **Git 형상 관리 및 코드 리뷰 불능**: UI 컴포넌트의 단순 수정에도 수만 자의 거대한 단일 라인 diff가 발생하여 PR 코드 리뷰가 불가능하고 머지 충돌(merge conflict) 수동 해결이 불가능하다.
2. **브라우저 캐싱 불가**: HTML, JS, CSS가 하나의 파일로 결합되어 있어 브라우저가 정적 자산(CSS/JS)을 독립적으로 캐시(`Cache-Control: immutable`, `ETag`)하지 못하고 매 페이지 로드 시 전체를 다시 전송받아 파싱해야 한다.
3. **보안 제약**: 인라인 스크립트와 스타일은 엄격한 Content Security Policy(CSP) 적용을 방해하며 `'unsafe-inline'` 정책을 강제한다.
4. **개발 생산성(DX) 저하**: 프론트엔드 코드 수정 시마다 번들 빌드와 Go 바이너리 재컴파일을 거쳐야 하므로 Vite가 제공하는 초고속 HMR(Hot Module Replacement) 개발 환경을 활용할 수 없다.

본 스펙의 목표는 **Go 바이너리의 Standalone 단일 파일 배포(Zero-dependency) 특성을 100% 보존하면서, 표준 Vite 정적 자산 분리(HTML/JS/CSS), Go 1.16+ `embed.FS` 기반 디렉토리 서빙, 브라우저 장기 캐싱, 그리고 로컬 Vite Dev Server 프록시 듀얼 모드를 도입하여 프론트엔드 에셋의 유지보수성과 개발 생산성을 현대화**하는 것이다.

### Intent and Goal IDs
- **INT-ASSETS-01**: 프론트엔드 빌드 결과물이 138KB 단일 라인 minified HTML로 git과 Go 소스 트리에 고정되어 있는 구조적 결함을 해소하고, 프론트엔드 에셋의 유지보수성, 브라우저 캐싱 성능, 개발자 생산성을 극대화한다.
- **GOAL-ASSETS-01**: Vite 빌드에서 `vite-plugin-singlefile`을 제거하고 표준 자산 분리(HTML 셸, JS 번들, CSS 번들)로 전환하여 Git diff 및 PR 코드 리뷰 가독성을 확보한다.
- **GOAL-ASSETS-02**: Go 1.16+ `embed.FS`를 도입하여 정적 자산 디렉토리를 임베드하고, HTTP 서빙 시 `index.html`은 `no-cache`, 해시된 정적 자산(`/assets/*`)은 장기 불변 캐시(`Cache-Control: public, max-age=31536000, immutable`)로 분리 서빙한다.
- **GOAL-ASSETS-03**: 개발 모드(`CODEFLOW_UI_DEV` 환경변수)를 통해 Vite 개발 서버(HMR 지원) 리버스 프록시를 지원하여 UI 개발 피드백 루프를 수초 이내로 단축한다.
- **GOAL-ASSETS-04**: Go CLI의 Standalone zero-dependency 단일 실행 바이너리 배포 특성을 100% 보존한다.

---

## 2. Scope

### In Scope
1. **Vite 빌드 파이프라인 개편**:
   - `web/flowview/vite.config.ts`: `vite-plugin-singlefile` 플러그인 제거 및 표준 `dist/` 빌드 설정 (`index.html`, `assets/[name]-[hash].js`, `assets/[name]-[hash].css`)
   - `web/flowview/package.json`: `vite-plugin-singlefile` 패키지 의존성 제거
2. **Go 프레젠터 정적 자산 임베드 현대화**:
   - `internal/presenter/flowview/`:
     - 138KB 단일 파일 `svelte_flow_view.html` 제거
     - `dist/` 디렉토리를 대상으로 한 Go 1.16+ `embed.FS` 정의 파일 도입 (`flow_view_assets.go`)
     - `server.go`: `http.FileServer` 기반 라우팅 및 `Cache-Control` 헤더 분리 적용
     - `server.go`: `CODEFLOW_UI_DEV` 환경변수 감지 시 Vite Dev Server(`http://localhost:5173` 등) 리버스 프록시 서빙 모드 추가
3. **빌드 및 패키징 스크립트 갱신**:
   - `Makefile`: `build-ui` 타깃을 단일 HTML 파일 복사 대신 `web/flowview/dist/`의 `internal/presenter/flowview/dist/` 디렉토리 동기화로 갱신
4. **테스트 및 검증 스위트 갱신**:
   - `internal/presenter/flowview/svelte_flow_view_test.go`: 단일 HTML 문자열 검사에서 `embed.FS` 기반 HTML 서빙 및 정적 에셋 서빙 검증으로 갱신
   - 전체 회귀 테스트 통과 검증 (`make test`, `make vet`, `make check-naming`)

### Non-Goals
- 외부 CDN 또는 원격 호스팅 의존성을 필수로 요구하는 배포 모델로의 전환 (오프라인/에어갭 환경 작동 필수)
- Svelte 5 프론트엔드 내부 비즈니스 로직(스토어, 컴포넌트 계층, 알고리즘) 변경
- 기존 FlowView REST API (`/api/task/view`, `/api/flow`, `/api/task/compare` 등) 또는 MCP JSON-RPC 프로토콜 변경
- FlowView UI에 컴파일러 내부 텔레메트리 노출 (Anti-Telemetry 규칙 엄격 준수)

---

## 3. Actors and Preconditions

- **Primary Actor**:
  - **프론트엔드 개발자**: FlowView UI를 개발/수정하며 빠른 피드백(HMR)과 명확한 Git diff를 필요로 함.
  - **최종 사용자/개발자**: `codeflow view` 또는 MCP를 통해 별도 Node.js 설치 없이 로컬 웹 UI를 사용하는 개발자.
- **Permissions**:
  - 로컬 파일시스템 읽기/쓰기 권한 및 로컬 루프백 네트워크 포트 바인딩 권한.
- **System Preconditions**:
  - Go 1.22+ 런타임 (표준 `embed.FS` 지원)
  - Node.js 18+ (UI 빌드 시에만 필요, CLI 실행 시에는 불필요)

---

## 4. Confirmed Facts

1. `web/flowview/index.html` 원본은 14줄의 경량 HTML 셸이며, Vite 빌드 시 `vite-plugin-singlefile`로 인해 138KB 단일 파일로 합쳐지고 있었다.
2. Go 1.16부터 표준 라이브러리로 제공되는 `embed.FS`와 `http.FS(io/fs.Sub(...))`를 사용하면 단일 바이너리 안에 서브 디렉토리 전체를 포함하고 표준 `http.FileServer`로 서빙할 수 있다.
3. `server.go`의 `serveIndex`는 현재 `/` 및 `/live` 경로에서 `SvelteFlowViewHTML` 문자열을 바이트로 단순 출력하고 있다.
4. `Makefile`의 `build-ui` 타깃은 현재 `cp web/flowview/dist/index.html internal/presenter/flowview/svelte_flow_view.html`을 수행하고 있다.

---

## 5. Assumptions

- **ASM-01**: 프론트엔드 빌드 결과물(`dist/`)을 `internal/presenter/flowview/dist/`에 두고 Go `embed.FS`로 패키징하면, Go 컴파일러는 추가 설정 없이 해당 에셋을 최종 바이너리에 포함한다.
  - Consequence if false: 바이너리 빌드 실패 또는 런타임 404 에러 발생.
  - Validation method: `go build` 후 바이너리 단독 실행으로 `/` 및 `/assets/*` 정상 응답 확인.
- **ASM-02**: Vite 개발 서버를 리버스 프록시할 때 웹소켓(HMR) 연결도 Go의 `httputil.ReverseProxy`를 통해 투명하게 전달된다.
  - Consequence if false: 개발 모드에서 자동 화면 갱신이 되지 않고 수동 새로고침이 필요할 수 있음.
  - Validation method: `CODEFLOW_UI_DEV` 설정 후 Svelte 컴포넌트 수정 시 브라우저 콘솔 및 화면 갱신 검증.

---

## 6. Business Rules and Invariants

- **INV-ASSETS-01 (Standalone Binary Guarantee)**: `codeflow` 빌드 결과물은 외부 네트워크나 로컬 Node.js 설치 없이 독립적으로 완전한 FlowView 웹 UI를 서빙할 수 있어야 한다.
- **INV-ASSETS-02 (Anti-Telemetry Guard)**: 에셋 서빙 헤더, 프록시 로깅, HTML 셸 등에 컴파일러 내부 에포크나 벤치마크 지표가 노출되지 않아야 한다.
- **INV-ASSETS-03 (E2E Contract Preservation)**: `index.html` 셸 및 번들 렌더링 후 DOM 마커(`data-view="flowview"`, `#execution-navigation`, `#code-flow`, `#context`, `#request-form`)는 반드시 보존되어야 한다.
- **INV-ASSETS-04 (Caching Invariant)**: HTML 파일(`index.html`)은 `no-cache`로 서빙되어 항상 최신 진입점을 보장해야 하고, 콘텐츠 해시가 포함된 자산(`/assets/*`)은 불변 캐시(`max-age=31536000, immutable`)로 서빙되어야 한다.

---

## 7. Observable User Flow

### 7.1 일반 사용자 (CLI 런타임)
1. 사용자가 `codeflow view` 또는 MCP `open_review`를 실행한다.
2. 백엔드 서버가 로컬 포트를 열고 브라우저가 접속한다.
3. 브라우저가 `GET /`을 요청하면 Go 서버의 `embed.FS`에서 1KB 미만의 `index.html`을 즉시 반환한다 (`Cache-Control: no-cache`).
4. 브라우저가 `index.html`에 명시된 `/assets/index-[hash].js` 및 `/assets/index-[hash].css`를 요청한다.
5. Go 서버가 장기 캐시 헤더(`immutable`)와 함께 정적 파일을 응답하고, 브라우저는 이를 로컬 디스크 캐시에 보관하여 후속 접속 시 네트워크 왕복 없이 즉시 로드한다.

### 7.2 UI 개발자 (HMR 개발 모드)
1. 개발자가 `web/flowview`에서 `npm run dev`를 실행하여 Vite 개발 서버(`http://localhost:5173`)를 구동한다.
2. 개발자가 `CODEFLOW_UI_DEV=http://localhost:5173 codeflow view`로 백엔드를 실행한다.
3. 브라우저 접속 시 Go 서버는 모든 UI 및 정적 자산 요청을 Vite 서버로 전달(Reverse Proxy)한다.
4. 개발자가 `.svelte` 또는 `.ts` 파일을 수정하면 Vite의 HMR에 의해 브라우저가 수 밀리초 만에 핫 리로드된다.

---

## 8. Failure and Boundary Behavior

1. **임베드 에셋 누락 (`dist/` 미존재)**:
   - 빌드 시 `internal/presenter/flowview/dist/`가 비어있거나 누락된 경우 컴파일 타임에 `//go:embed` 에러가 발생하여 불완전한 바이너리 생성을 원천 차단한다.
2. **잘못된 정적 파일 요청 (404 Not Found)**:
   - `/assets/non-existent.js` 요청 시 올바른 404 HTTP 상태 코드와 함께 깔끔한 텍스트 에러를 반환한다.
3. **Dev Server 프록시 대상 미구동**:
   - `CODEFLOW_UI_DEV`가 지정되었으나 해당 주소(예: `localhost:5173`)에 서버가 구동되지 않은 경우, 502 Bad Gateway 응답과 함께 "Vite 개발 서버가 실행 중인지 확인하세요"라는 친절한 가이드를 제공한다.

---

## 9. Decisions

| ID | Question | Decision | Rationale | Rejected Alternative | Consequences | Source / Evidence | Decision Record |
|---|---|---|---|---|---|---|---|
| D-01 | 인라인 번들을 어떤 방식으로 분리할 것인가? | Vite 표준 번들링 및 Go `embed.FS` 도입 | Git diff 가독성 확보, 브라우저 장기 캐싱 활성화, 단일 바이너리 배포 원칙 유지 | 단일 HTML 유지, 외부 CDN 분리 | `dist/` 디렉토리 관리 및 서빙 라우팅 필요 | 사용자 문제 제기 및 코드베이스 분석 | [`DEC-FLOWVIEW-EMBEDDED-ASSETS-REFACTORING-01`](../decisions/2026-09-23-flowview-embedded-assets-refactoring-ko.md) |
| D-02 | 브라우저 캐싱 정책을 어떻게 적용할 것인가? | HTML은 `no-cache`, 해시된 정적 자산은 1년 불변(`immutable`) 캐싱 | HTML 변경 시 즉시 새 번들을 참조하도록 보장하면서 정적 자산은 네트워크 비용 0으로 재사용 | 모든 요청 무캐싱, 모든 요청 만료시간 동일 적용 | 서빙 핸들러에서 경로별 캐시 헤더 분기 처리 | 웹 표준 캐싱 가이드라인 | [`DEC-FLOWVIEW-EMBEDDED-ASSETS-REFACTORING-01`](../decisions/2026-09-23-flowview-embedded-assets-refactoring-ko.md) |
| D-03 | 프론트엔드 개발 시 피드백 루프를 어떻게 단축할 것인가? | `CODEFLOW_UI_DEV` 환경변수를 통한 Vite Dev Server 리버스 프록시 모드 지원 | 개발자가 번들 빌드와 Go 재컴파일 없이 Svelte HMR의 즉각적인 화면 갱신을 누릴 수 있음 | Go 파일 변경 감지 후 자동 빌드-카피 스크립트 | `httputil.ReverseProxy` 기반 프록시 핸들러 추가 | DX 개선 요구 및 기존 아키텍처 분석 | [`DEC-FLOWVIEW-EMBEDDED-ASSETS-REFACTORING-01`](../decisions/2026-09-23-flowview-embedded-assets-refactoring-ko.md) |

---

## 10. Quality Constraints

1. **바이너리 독립성**: 외부 npm/node 의존성 없이 `go build`된 바이너리만으로 완벽한 로컬 FlowView 제공.
2. **빌드 재현성**: `make build-ui && make build` 수행 시 항상 일관된 정적 파일 번들이 생성되어야 함.
3. **HTTP 표준 준수**: 모든 정적 파일 응답에 적합한 `Content-Type`(`text/html`, `application/javascript`, `text/css` 등) 및 `Cache-Control` 명시.

---

## 11. Feature-Level Acceptance

- **FA-01**: WHEN `make build-ui`를 실행할 때, THE 빌드 시스템은 `vite-plugin-singlefile` 없이 표준 `dist/` 번들을 생성하고 `internal/presenter/flowview/dist/`에 유효한 `index.html`과 `assets/` 디렉토리를 동기화해야 한다.
- **FA-02**: WHEN 브라우저가 `GET /` 또는 `GET /live`를 요청할 때, THE 서버는 `Cache-Control: no-cache` 헤더와 함께 1KB 미만의 경량 `index.html` 셸을 HTTP 200으로 반환해야 한다.
- **FA-03**: WHEN 브라우저가 `GET /assets/*` 경로의 정적 자산을 요청할 때, THE 서버는 `Cache-Control: public, max-age=31536000, immutable` 헤더 및 올바른 MIME 타입을 응답해야 한다.
- **FA-04**: WHERE `CODEFLOW_UI_DEV` 환경변수가 설정되어 있을 때, THE 서버는 HTML 및 정적 자산 요청을 지정된 프록시 타깃으로 투명하게 전달해야 한다.
- **FA-05**: THE `internal/presenter/flowview` 패키지 단위 테스트 및 전체 프로젝트 빌드(`make test`, `make vet`)가 오류 없이 통과해야 한다.

---

## 12. Open Decisions

| ID | Question | Options | Trade-off | Recommendation |
|---|---|---|---|---|
| Q-01 | `internal/presenter/flowview/dist/`를 Git에 추적(commit)할 것인가, `.gitignore` 처리할 것인가? | 1. Git 추적 유지 (사전 빌드된 번들 커밋)<br>2. Git 제외 (`.gitignore`) 후 CI/Make에서만 빌드 | 옵션 1은 Node.js가 없는 개발자도 `go install`이나 `go build`가 가능함. 옵션 2는 레포지토리가 완벽히 깨끗해지지만 Node.js가 필수 빌드 도구가 됨. | **(권장) 옵션 1 유지하되 표준 분리된 파일로 커밋**: 번들이 JS/CSS로 분리되면 HTML 셸의 diff는 깨끗해지며, Node.js가 없는 순수 Go 개발자도 즉시 `go build` 가능함. 추후 릴리즈 자동화 성숙 시 옵션 2로 전환 고려. |

---

## 13. Done When

- `docs/design/specs/2026-09-23-flowview-embedded-assets-refactoring-ko.md` 및 관련 결정 레코드가 정규화됨.
- `web/flowview`에서 `vite-plugin-singlefile`이 제거되고 표준 번들 생성이 검증됨.
- `internal/presenter/flowview`에서 `embed.FS` 기반 자산 서빙 및 캐싱 헤더가 적용됨.
- `CODEFLOW_UI_DEV` 프록시 동작이 검증됨.
- `make build-ui`, `make build`, `make test`, `make vet` 전체 성공.
