import { test, expect } from './approval-fixture';
import AxeBuilder from '@axe-core/playwright';

test.describe('FlowView Accessibility Audit', () => {
  test('approval lifecycle is keyboard operable and announces committed results', async ({ page, approvalFixture }) => {
    await approvalFixture.open(page);
    const approve = page.getByRole('button', { name: 'Approve semantic proposal', exact: true });
    await approve.focus();
    await expect(approve).toBeFocused();
    await approve.press('Enter');
    await expect(page.locator('#approval-history-state')).toHaveText('active');
    await expect(page.locator('#approval-result-msg')).toHaveAttribute('role', 'status');
    await expect(page.locator('#approval-result-msg')).toHaveAttribute('aria-live', 'polite');
    await expect(page.locator('#approval-result-msg')).toContainText('승인 기록 생성됨');
    await page.getByRole('textbox', { name: 'Edited semantic proposal text' }).fill('Keyboard reviewed explanation');
    const edit = page.getByRole('button', { name: 'Edit and approve semantic proposal', exact: true });
    await edit.focus();
    await edit.press('Space');
    await expect(page.locator('#approval-history-version')).toHaveText('2');
    const revoke = page.getByRole('button', { name: 'Revoke active semantic approval', exact: true });
    await revoke.focus();
    await revoke.press('Enter');
    await expect(page.locator('#approval-history-state')).toHaveText('revoked');
    await expect(page.locator('#approval-result-msg')).toContainText('(revoke)');
    await expect(approve).toBeDisabled();
    const scan = await new AxeBuilder({ page }).include('#semantic-approval-section').withTags(['wcag2a', 'wcag2aa']).analyze();
    expect(scan.violations.filter(v => v.impact === 'critical' || v.impact === 'serious')).toEqual([]);
  });

  test('should pass automated accessibility audit', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');
    await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');

    const accessibilityScanResults = await new AxeBuilder({ page })
      .withTags(['wcag2a', 'wcag2aa'])
      .analyze();

    const criticalViolations = accessibilityScanResults.violations.filter(
      v => v.impact === 'critical' || v.impact === 'serious'
    );

    expect(criticalViolations).toEqual([]);
  });

  test('keeps candidate authority and projection state explicit', async ({ page }) => {
    await page.goto('http://127.0.0.1:4589/?token=testtoken');
    await page.locator('#query-input').fill('HomePage.handleQuickCheckout');
    await page.locator('#query-submit').click();

    await expect(page.locator('#current-answer-strip')).toBeVisible({ timeout: 10000 });
    await expect(page.locator('#current-answer-stage')).not.toContainText('Verified');
    await expect(page.locator('#current-answer-agent-status')).toHaveText('not reported');
    await expect(page.locator('#current-answer-statement')).toContainText('Evidence-backed Implementation Fact:');
    await expect(page.locator('#projection-summary')).toContainText(/folds:|unknown boundaries:/);
    await expect(page.locator('#badge-freshness')).toHaveText(/historical|unknown|후보 basis|미확인/i);
    await expect(page.locator('#badge-enrichment')).toHaveText('Enrichment: unavailable');
    await expect(page.locator('#proposal-card')).toHaveAttribute('data-epistemic-status', 'unknown');
    await expect(page.locator('#enrichment-fallback')).toBeVisible();
    await expect(page.locator('#model-activation-disclosure')).toBeHidden();

    // The onboarding controls are usable from the keyboard and disclose the
    // immutable basis/coverage state rather than presenting an unlabeled
    // synthetic domain list.
    await page.locator('#btn-explore-domains').click();
    await expect(page.locator('#onboarding-evidence-status')).toContainText('basis:');
    await expect(page.locator('#onboarding-evidence-status')).toContainText('generation:');
    const domainButton = page.locator('#domain-cards-grid button[aria-label]').first();
    await expect(domainButton).toBeVisible();
    await expect(domainButton).toHaveAttribute('aria-label', /도메인/);
    await domainButton.focus();
    await domainButton.press('Enter');
    await expect(page.locator('#onboarding-catalog-container')).toBeVisible();
  });
});
