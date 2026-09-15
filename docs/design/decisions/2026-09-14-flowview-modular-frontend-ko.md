# FlowView 모듈형 프론트엔드 아키텍처 결정기록

- Decision ID: `DEC-FLOWVIEW-MODULAR-FRONTEND-01`
- Status: Approved
- Date: 2026-09-14
- Scope: FlowView 단일 템플릿의 하드코딩 탈피, 컴포넌트 모듈화, Storyboard 및 Blast Radius Radar 아키텍처
- Related Spec: `docs/design/specs/2026-09-14-flowview-modular-frontend-ko.md`

## 맥락과 문제

기존 FlowView는 4,200줄에 달하는 레거시 화면(`flow_view.html`)과 압축형 실험용 프로토타입(`live-semantic-map-prototype.html`)으로 이원화되어 있었으며, 후자는 264줄 단일 파일에 CSS/HTML/JS가 밀집된 인라인 스크립트 형태로 작성되어 있었다.
FlowView가 기존 레거시를 탈피하여 `live-semantic-map-prototype.html`을 표준 단일 기반 화면으로 전면 채택함에 따라, `live-semantic-map-final.html`에서 검증된 두 가지 핵심 시각화 기능(MACRO CONTEXT STORYBOARD, BLAST RADIUS RADAR)을 추가해야 한다.
그러나 하드코딩된 목데이터와 인라인 DOM 제어 방식에 신규 기능을 덧붙일 경우 코드 가독성 저하와 스파게티화가 심화되므로, 외부 번들러나 런타임 종속성 없이 단일 템플릿 계약을 유지하면서 재사용 가능하고 유지보수 가능한 컴포넌트 모듈형 구조로 전면 개선해야 한다.

## 결정 사항

1. **단일 템플릿 계약 및 파이프라인 유지**:
   - `docs/samples/live-semantic-map-prototype.html`을 유일한 표준 템플릿 원본으로 유지한다.
   - `scripts/generate_live_semantic.py`의 `// SAMPLE_BOOTSTRAP` 분리 방식을 보존하여 빌드 시 `internal/flowview/live_view.html`로 자동 동기화되고 Go 테스트(`TestLiveViewMatchesDesignatedTemplate`)를 통과하도록 한다.

2. **컴포넌트 기반 아키텍처 (Vanilla Component Pattern) 채택**:
   - Webpack/Vite 등 무거운 외부 프론트엔드 빌드 도구를 강제하지 않고, 브라우저 표준 기술만으로 동작하는 경량 네임스페이스 컴포넌트 패턴(`CodeFlow.*`)을 적용한다.
   - UI를 `StoryboardLane`, `BlastRadiusRadarWidget`, `NavRail`, `FlowCardPanel`, `ContextAside` 등 명확한 독립 렌더러로 분리한다.

3. **단일 반응형 상태 머신 (Single State Store) 구축**:
   - 컴포넌트 간 직접적인 DOM 참조나 얽힌 이벤트 리스너를 금지하고, `CodeFlow.Store`를 통해 `selectedStepId`, `viewMode`, `activeData`, `impactData`를 관리한다.
   - 상단 스토리보드 클릭, 좌측 레일 클릭, 중앙 코드 카드 스크롤, 우측 레이더 노드 선택이 단일 `dispatch('SELECT_STEP', stepId)` 액션을 통해 완전히 동기화되도록 보장한다.

4. **Blast Radius Radar의 계층형 데이터 결합 및 우아한 폴백 (Graceful Fallback)**:
   - 레이더는 백엔드의 `GET /api/task/impact` API(`ChangeImpactGraph`)와 연동하여 1차 링(직접 호출자/상태 변경), 2차 링(간접 호출자), 3차 링(테스트)을 순수 SVG로 동적 렌더링한다.
   - 오프라인/샘플 모드 또는 백엔드 테스트 인덱싱 부재 시에는 `semanticMap.edges`의 직접 호출자 데이터를 기반으로 1차 링을 구성하고 외곽 링은 "근거 미확인 경계"로 정직하게 표기한다.

5. **Anti-Telemetry Guard 엄격 준수**:
   - `live-semantic-map-final.html`에 존재하던 내부 텔레메트리/추정치(예: `Intent Convergence 67%`, `Invariant Proof: Guaranteed`, 에포크, 랙 등)를 철저히 배제하고, 오직 검증된 소스 코드 심볼, 계층, 호출 및 테스트 관계만을 시각화한다.

## 대안 검토 및 기각 이유

- **대안 A: React/Svelte 및 Vite 기반 SPA로 전면 전환**
  - 기각 이유: Go 단일 바이너리 배포 및 `//go:embed` 단일 HTML 서빙 구조를 깨뜨리며, Node.js 빌드 파이프라인 종속성이 추가되어 CLI/MCP 배포 및 검증 복잡도가 불필요하게 급증함.
- **대안 B: 기존 `live-semantic-map-prototype.html` 코드에 인라인 DOM 함수 추가**
  - 기각 이유: 화면이 동작하더라도 템플릿의 가독성과 확장성이 극도로 저하되어 향후 기능 개선 및 유지보수가 불가능해짐.

## 결과 및 기대 효과

- 유지보수성과 가독성이 대폭 향상된 깔끔한 단일 파일 모듈형 템플릿 확보
- 엔드투엔드 거시 흐름(Storyboard)과 미시 파급 영향(Radar)의 완벽한 양방향 동기화 제공
- 기존 Go 파이프라인 및 테스트 계약 100% 보존
