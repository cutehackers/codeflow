<script lang="ts">
 import { conditionOutcomeLabel } from '../stores/conditionNavigation';
 import { flowStore, EDGE_LABELS } from '../stores/flowStore.svelte';
</script>
<section class="connections" aria-label="요청 흐름 안의 전후 연결">
 <header><strong>이 코드의 전후 연결</strong><small>{flowStore.data?.sourceContextMissing ? '입력된 연결 · 소스 근거 미확인' : '실제 실행 기록이 아닌 소스 연결'}</small></header>
 <div class="columns">
 {#each [{title:'이곳으로 연결',relations:flowStore.incomingRelations,incoming:true},{title:'이곳에서 연결',relations:flowStore.outgoingRelations,incoming:false}] as group}
 <div><h3>{group.title}</h3>
 {#each group.relations as relation (relation.id)}
 {@const step = group.incoming ? relation.fromStep : relation.toStep}
 {#if relation.resolved && step}<button data-navigation-focus={`${group.incoming ? "incoming" : "outgoing"}:${relation.id}`} onclick={() => {flowStore.saveNavigationState(); flowStore.select(step.stepId)}}>{group.incoming ? '←' : '→'} {step.name}<small>{EDGE_LABELS[relation.edge.kind] || relation.edge.kind}{#if relation.edge.conditions?.[0]} · {conditionOutcomeLabel(relation.edge.conditions[0].outcome)}{/if}</small></button>
 {:else}<p class="unknown">연결 미확인 · {relation.edge.toSymbolPath || '대상 미확인'}</p>{/if}
 {:else}<p>분석에서 확인한 연결 없음</p>{/each}
 </div>{/each}
 </div>
 {#if flowStore.selectedStep?.branch}<p class="condition">현재 단계의 조건 · {flowStore.selectedStep.branch}</p>{/if}
</section>
<style>
 .connections{border:1px solid #ddd;border-radius:6px;padding:14px;margin-bottom:16px;font-size:12px}header{display:flex;flex-wrap:wrap;justify-content:space-between;gap:4px;margin-bottom:12px}small{color:#666;font-size:10px}.columns{display:grid;grid-template-columns:1fr 1fr;gap:16px}h3{font-size:11px;margin-bottom:6px}.columns button{display:block;width:100%;text-align:left;font-size:11px;margin:4px 0;overflow-wrap:anywhere}.columns button small{display:block;color:inherit}.columns p{font-size:11px;color:#666}.unknown{border-bottom:1px dashed #999}.condition{border-top:1px solid #ddd;margin-top:10px;padding-top:10px;overflow-wrap:anywhere}
</style>
