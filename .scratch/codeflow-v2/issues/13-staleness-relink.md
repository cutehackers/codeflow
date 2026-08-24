# 13: stale 판정 + 앵커 relink (fusion 분할 3/3)

**What to build:** 재발행 시점의 현재성 관리. 코드 변경 후 앵커를 enclosingSymbolPath + canonical AST fingerprint로 재연결(relink)한다. 라인만 밀린 사소한 변경은 orphan 아님(재승인 피로도 방지), 동작 변경(AST fingerprint 불일치)은 approved → stale 전환 + 승인 큐 대상 플래그.

**Blocked by:** 10 (증분 재발행 트리거), 12 (fusion 코어).

**Status:** ready-for-agent

- [ ] 포맷팅만 변경 시 orphan 없이 relink됨
- [ ] 로직 변경 시 stale 전환 + 큐 플래그
- [ ] freshness 필드가 flowspec에 정확 반영됨
