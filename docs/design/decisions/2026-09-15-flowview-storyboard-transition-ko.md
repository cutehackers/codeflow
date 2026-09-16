# FlowView Storyboard 전환 결정

- Status: Accepted for `FLOWVIEW-STORYBOARD-TRANSITION`
- Created: 2026-09-15

## 결정1. 화면의 기본 단위

Storyboard frame을 기본 단위로 사용한다. function, file, package, architecture 정보는 장면을 분리하는 기준이 아니다. 이유는 프로젝트 구조가 달라도 처리 사건과 결과 변화는 공통으로 읽을 수 있기 때문이다.

## 결정2. 응답과 선택 안정성

기존 task-view 결과의 의미를 유지하면서 versioned `storyboard` field를 추가한다. 재분석 뒤에는 정규화된 `frameMatchKey`가 정확히 하나인 경우만 선택을 복원한다. UI가 raw map에서 장면을 다시 조합하거나 제목·순번으로 장면을 추정하지 않는다.

## 결정3. 갱신과 삭제

분석과 비교는 사용자의 명시적 명령으로만 실행한다. Live 전용 기능은 별도 제거 slice가 caller·consumer, 공개 seam, 보존 대상, 기존 계약 reference를 inventory한 후 삭제한다. 이 결정은 기존 계약을 자동 수정하거나 삭제하지 않는다.
