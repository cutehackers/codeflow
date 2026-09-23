import type { FlowTaskViewData, SemanticDelta } from '../types/flow';

export function samplePayload(version: 1 | 2 = 1): FlowTaskViewData {
  const generationId = 'sample-v' + version;
  const snapshotId = 'sample-snapshot-v' + version;

  const source = [
    {
      id: 'checkout_click',
      name: '고객 결제 버튼 클릭',
      desc: '화면에서 장바구니 품목 취합 후 체크아웃 이벤트 트리거',
      symbol: 'HomePage.checkout()',
      layer: 'ui_event',
      path: 'ui/home_page.ts',
      start: 12,
      hit: 14,
      lines: [
        'async checkout() {',
        '  const items = cartStore.getSelectedItems();',
        '  const validation = await validateCartUseCase.execute(items);',
        '  if (!validation.ok) {',
        '    this.showCartError(validation.error);',
        '    return;',
        '  }',
        '}'
      ]
    },
    {
      id: 'validate_cart',
      name: '장바구니 정합성 검증',
      desc: '수량 0 여부, 비정상 품목 스키마 사전 필터링',
      symbol: 'ValidateCartUseCase',
      layer: 'gateway',
      path: 'application/validate_cart_usecase.ts',
      start: 24,
      hit: 26,
      branch: '품목 수량 <= 0 또는 스키마 불일치 → ValidationError 반환',
      lines: [
        'async execute(items: CartItem[]): Promise<ValidationResult> {',
        '  if (!items.length || items.some(i => i.quantity <= 0)) {',
        '    return { ok: false, error: "유효하지 않은 장바구니 품목" };',
        '  }',
        '  return await inventoryService.checkStock(items);',
        '}'
      ]
    },
    {
      id: 'check_stock',
      name: '실시간 재고 검증 & 선점',
      desc: '품목 잔여량 확인, 10분간 선점 락 부여 및 토큰 발행',
      symbol: 'InventoryService.checkStock()',
      layer: 'domain_core',
      path: 'domain/inventory_service.ts',
      start: 38,
      hit: 41,
      branch: 'availableQty < requestedQty → OutOfStockException 발생',
      lines: [
        'async checkStock(items: CartItem[]): Promise<ReservationToken> {',
        '  const available = await stockRepo.getAvailableQuantities(items);',
        '  if (available < items.length) {',
        '    throw new OutOfStockException("실시간 재고가 부족합니다");',
        '  }',
        '  const lockToken = await stockRepo.acquireReservationLock(items, 600);',
        '  return lockToken;',
        '}'
      ]
    },
    {
      id: 'create_order',
      name: '주문서 생성 & 승인 대기',
      desc: '예약 토큰으로 임시 주문서 발급, 결제 대기 상태 승격',
      symbol: 'CreateOrderUseCase',
      layer: 'application',
      path: 'application/create_order_usecase.ts',
      start: 50,
      hit: 53,
      lines: [
        'async execute(items: CartItem[], token: ReservationToken) {',
        '  const order = Order.createDraft(items, token.id);',
        '  order.promoteToPendingPayment();',
        '  await orderRepository.save(order);',
        '  return await paymentGateway.authorize(order);',
        '}'
      ]
    },
    {
      id: 'pg_authorize',
      name: 'PG사 결제 승인 API',
      desc: '선점된 주문 금액으로 외부 PG 결제 트랜잭션 호출',
      symbol: 'PaymentGateway.authorize()',
      layer: 'external_pg',
      path: 'external/payment_gateway.ts',
      start: 62,
      hit: 64,
      sideEffect: '외부 PG 결제 승인 API(/v1/payments/authorize) 트랜잭션 호출',
      lines: [
        'async authorize(order: Order): Promise<PaymentReceipt> {',
        '  const payload = { orderId: order.id, amount: order.totalAmount };',
        '  const receipt = await pgClient.post("/v1/payments/authorize", payload);',
        '  return receipt;',
        '}'
      ]
    }
  ];

  const steps = source.map((s, i) => ({
    stepId: s.id,
    structuralIdentity: s.id,
    ordinal: i + 1,
    name: s.name,
    description: s.desc,
    technicalName: s.symbol,
    layer: s.layer,
    branch: s.branch,
    sideEffect: s.sideEffect,
    anchor: { repoRelativePath: s.path, enclosingSymbolPath: s.symbol }
  }));

  const edge = (from: string, to: string, kind = 'call') => ({
    fromStepId: from,
    toStepId: to,
    kind,
    resolutionStatus: 'resolved' as const
  });

  const sampleDelta: SemanticDelta = {
    changes: [
      { kind: 'changed_rule', targetStepId: 'validate_cart', summary: '장바구니 사전 정합성 검증 규칙 변경' },
      { kind: 'added_behavior', targetStepId: 'check_stock', summary: '10분간 실시간 재고 선점 락 부여 및 토큰 발행 수술' }
    ]
  };

  const sampleImpacts = {
    check_stock: {
      directImpact: {
        callers: [{ name: 'ValidateCartUseCase', symbolPath: 'ValidateCartUseCase' }],
        stateMutations: [{ targetState: 'StockLock.ACTIVE' }],
        tests: [{ testSymbolPath: 'checkout_test.ts', broken: true }]
      },
      indirectImpact: {
        callers: [{ name: 'HomePage', symbolPath: 'HomePage.checkout' }]
      }
    },
    checkout_click: {
      directImpact: {
        callers: [{ name: 'CheckoutButton', symbolPath: 'CheckoutButton.onClick' }],
        stateMutations: [],
        tests: [{ testSymbolPath: 'home_page_test.ts' }]
      },
      indirectImpact: { callers: [] }
    },
    validate_cart: {
      directImpact: {
        callers: [{ name: 'HomePage', symbolPath: 'HomePage.checkout' }],
        stateMutations: [],
        tests: [{ testSymbolPath: 'validate_cart_test.ts' }]
      },
      indirectImpact: { callers: [] }
    },
    create_order: {
      directImpact: {
        callers: [{ name: 'InventoryService', symbolPath: 'InventoryService.checkStock' }],
        stateMutations: [{ targetState: 'OrderState.PENDING_PAYMENT' }],
        tests: [{ testSymbolPath: 'order_test.ts' }]
      },
      indirectImpact: { callers: [] }
    },
    pg_authorize: {
      directImpact: {
        callers: [{ name: 'CreateOrderUseCase', symbolPath: 'CreateOrderUseCase' }],
        stateMutations: [{ targetState: 'PaymentGateway.TRANSACTION' }],
        tests: [{ testSymbolPath: 'pg_gateway_test.ts' }]
      },
      indirectImpact: { callers: [] }
    }
  };

  const flowSequenceFrames = source.map((s, idx) => {
    let role: 'entry' | 'decision' | 'process' | 'effect' | 'result' | 'boundary' = 'process';
    if (idx === 0) role = 'entry';
    else if (s.branch) role = 'decision';
    else if (s.sideEffect) role = 'effect';
    else if (idx === source.length - 1) role = 'result';

    return {
      frameID: `frame-${String(idx + 1).padStart(2, '0')}`,
      ordinal: idx + 1,
      role,
      title: s.name,
      text: s.desc,
      technicalAnchor: s.symbol,
      stepRefs: [s.id],
      primaryStepRef: s.id,
      sourceAnchor: {
        repoRelativePath: s.path,
        enclosingSymbolPath: s.symbol
      },
      condition: s.branch,
      outcomes: role === 'decision' ? ['success', 'failure'] : ['success'],
      status: 'verified' as const,
      frameMatchKey: `${role}|${s.symbol}`
    };
  });

  const flowSequence = {
    schemaId: 'https://codeflow.local/schemas/flow_sequence.schema.json',
    schemaVersion: 1,
    flowID: `flow-sample-${version}`,
    generationId,
    computedBasisId: generationId,
    snapshotID: snapshotId,
    frames: flowSequenceFrames
  };

  return {
    semanticMap: {
      generationId,
      validatedAgainstSnapshotId: snapshotId,
      summary: { requested: '고객 결제 요청에서 PG사 승인까지 5개 관문 엔드투엔드 시퀀스' },
      steps,
      edges: [
        edge('checkout_click', 'validate_cart'),
        edge('validate_cart', 'check_stock'),
        edge('check_stock', 'create_order'),
        edge('create_order', 'pg_authorize'),
        edge('pg_authorize', 'create_order', 'return'),
        edge('create_order', 'checkout_click', 'return')
      ]
    },
    flowSequence,
    semanticDelta: sampleDelta,
    sampleImpacts,
    flowContexts: Object.fromEntries(source.map(s => [s.id, {
      stepId: s.id,
      generationId,
      snapshotId,
      canonicalPath: s.path,
      precision: 'exact',
      sourceLimitation: '설명용 비즈니스 관문 소스입니다. 실제 프로젝트 분석 결과가 아닙니다.',
      displayedLines: s.lines.map((text, i) => ({
        lineNumber: s.start + i,
        text,
        isHit: s.start + i === s.hit,
        isStruct: text.includes('if (') || text.includes('throw ')
      }))
    }])),
    unknowns: [{ reason: '이 샘플은 외부 PG사 네트워크 통신 내부 처리를 관측하지 않았습니다. 트랜잭션 타임아웃은 시뮬레이션되지 않았습니다.' }]
  };
}
