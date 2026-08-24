# 07: 동일 파일 구조 슬라이싱 + codeflow flow show

**What to build:** Dart 어댑터의 slice(): guard(IfStatement)/상태 mutation/throw/await/business-call 수집, UI 노이즈 denylist(TextStyle 등), secret redaction, 경계 마커(Repository·ApiClient) 도달 시 외부 호출 카드 기록 후 종료, depth 초과 truncated 플래그. CLI `codeflow flow show <id>`로 step 타임라인을 텍스트로 미리보기 — FlowView 없이도 검증 가능한 수직 슬라이스.

**Blocked by:** 06 (harvest가 후보를 공급).

**Status:** ready-for-agent

- [ ] 단일 파일 흐름에서 규칙·분기·상태변경 카드 추출
- [ ] 경계 마커 도달 시 종료 + 외부 호출 카드
- [ ] denylist 폐기와 redaction 적용 확인
- [ ] `codeflow flow show`로 타임라인 미리보기 출력
