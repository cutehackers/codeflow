<script lang="ts">
  import { flowStore } from '../stores/flowStore.svelte';
  import { samplePayload } from '../stores/sampleData';

  interface Props {
    title?: string;
    mode?: string;
    staticLinkUrl?: string;
    staticLinkLabel?: string;
  }

  let {
    title = 'CodeFlow',
    mode = 'FlowView Storyboard',
    staticLinkUrl = '',
    staticLinkLabel = ''
  }: Props = $props();

  function onReset() {
    flowStore.receive(samplePayload(1));
  }
</script>

<header class="bar">
  <div
    class="brand"
    role="button"
    tabindex="0"
    onclick={onReset}
    onkeydown={(e) => e.key === 'Enter' && onReset()}
    style="cursor: pointer"
    title="온보딩 허브로 이동"
  >
    <b aria-hidden="true">cf</b>
    <span>{title}</span>
  </div>
  <span class="mode">{mode}</span>
  {#if staticLinkUrl}
    <a id="static-link" href={staticLinkUrl}>{staticLinkLabel}</a>
  {/if}
</header>

<style>
  .bar {
    display: flex;
    gap: 16px;
    align-items: center;
    height: 60px;
    padding: 0 30px;
    border-bottom: 1px solid var(--line, #dddddd);
    background: #ffffff;
  }
  .brand {
    font-size: 16px;
    font-weight: 750;
    display: flex;
    gap: 9px;
    align-items: center;
  }
  .brand b {
    background: #171717;
    color: #ffffff;
    display: grid;
    place-items: center;
    width: 27px;
    height: 27px;
    border-radius: 5px;
    font-size: 12px;
  }
  .mode {
    border-left: 1px solid #dddddd;
    padding-left: 16px;
    font-size: 12px;
    font-weight: 650;
  }
  .bar a {
    margin-left: auto;
    font-size: 11px;
    color: #555555;
    text-decoration: none;
  }
  .bar a:hover {
    text-decoration: underline;
  }

  @media (max-width: 650px) {
    .bar {
      padding: 0 15px;
      gap: 10px;
    }
    .mode {
      font-size: 10px;
      padding-left: 10px;
    }
    .bar a {
      font-size: 9px;
    }
  }
</style>
