# Decision: FlowView 정적 에셋 분리 및 Go embed.FS 서빙 현대화

- Decision ID: `DEC-FLOWVIEW-EMBEDDED-ASSETS-REFACTORING-01`
- Status: Approved
- Date: 2026-09-23
- Context:
  - 현재 FlowView 프론트엔드는 `vite-plugin-singlefile`을 통해 모든 Svelte 5 런타임, 컴포넌트 JS, CSS를 138KB가 넘는 단일 라인 minified 문자열로 인라인화하여 `internal/presenter/flowview/svelte_flow_view.html`에 저장하고, Go에서 `//go:embed svelte_flow_view.html`로 임베드하고 있음.
  - 이로 인해 UI의 미세한 변경에도 Git diff가 수만 자에 달해 PR 리뷰가 불가능하고, 머지 충돌 시 수동 해결이 불가능함.
  - 또한 브라우저가 JS와 CSS를 개별적으로 캐싱(`Cache-Control: immutable`, `ETag`)하지 못해 매 요청마다 138KB 전체를 새로 파싱해야 하며, 엄격한 Content Security Policy(CSP) 적용 시 `'unsafe-inline'`이 강제되는 보안 한계가 존재함.
  - 로컬 UI 개발 시에도 번들 빌드와 Go 재컴파일을 매번 거쳐야 하므로 Vite의 초고속 HMR(Hot Module Replacement)을 활용하지 못하는 개발 생산성(DX) 저하가 발생함.
- Decision:
  1. `web/flowview/vite.config.ts`에서 `vite-plugin-singlefile`을 제거하고 표준 멀티 파일 번들 빌드(`dist/index.html`, `dist/assets/*.js`, `dist/assets/*.css`)로 전환한다.
  2. `internal/presenter/flowview/`에서 단일 거대 파일 `svelte_flow_view.html`을 제거하고, Go 1.16+ `embed.FS`를 사용하여 빌드된 `dist/` 디렉토리를 임베드한다.
  3. `server.go`에서 `http.FileServer` 기반 서빙을 도입하고, `index.html`은 `Cache-Control: no-cache`, `/assets/*`는 `Cache-Control: public, max-age=31536000, immutable`로 분리하여 브라우저 장기 캐싱을 활성화한다.
  4. 로컬 개발 환경 편의를 위해 `CODEFLOW_UI_DEV` 환경변수가 지정된 경우 Vite Dev Server(`http://localhost:5173`)로 요청을 포워딩하는 리버스 프록시 모드를 지원한다.
- Alternatives Considered:
  - **대안 1: 현재의 단일 인라인 HTML 유지**:
    - 장점: Go 코드에서 단순 `string` 변수 하나로 서빙 가능.
    - 기각 사유: Git 형상 오염 심각, 코드 리뷰 불가, 브라우저 캐싱 불가, 보안 정책 제약 등의 단점이 장점을 압도함.
  - **대안 2: 외부 CDN 또는 원격 호스팅 자산 참조**:
    - 장점: Go 바이너리 크기 감소.
    - 기각 사유: 네트워크가 격리된 온프레미스/오프라인 환경에서 CLI가 동작하지 않게 되며, CodeFlow의 핵심 원칙인 '독립 실행형 로컬 도구' 요건을 위반함.
  - **대안 3 (선택안): Go `embed.FS` + 표준 에셋 분리 + Dev 프록시 지원**:
    - 장점: 단일 바이너리 배포 원칙을 완벽히 유지하면서 Git diff 클린화, 브라우저 캐싱 극대화, Vite HMR 개발 속도 확보를 동시에 달성함.
- Consequences:
  - 장점:
    - `index.html`이 1KB 미만의 깔끔한 셸로 유지되어 Git diff 및 PR 리뷰가 투명해짐.
    - 번들 JS/CSS가 분리되어 브라우저가 불변 캐싱을 적용하므로 FlowView 반복 로딩 속도가 비약적으로 향상됨.
    - UI 개발 시 `CODEFLOW_UI_DEV`를 통해 실시간 HMR로 빠른 피드백 루프를 얻을 수 있음.
  - 주의점:
    - `internal/presenter/flowview/svelte_flow_view_test.go`에서 기존의 단일 HTML 문자열 직접 매칭 로직을 `embed.FS` 및 `index.html`/번들 검증 로직으로 갱신해야 함.
    - `Makefile`의 `build-ui` 타깃을 단일 파일 복사에서 `dist/` 디렉토리 동기화로 변경해야 함.
