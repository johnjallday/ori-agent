import { test } from '@playwright/test';
import { verifySavedFirstLook } from './helpers/folder-saved-report';

// Recheck only the read-only tail of a previously executed live journey. This
// supplements, never replaces, fresh review/staffing/model-execution evidence.
const sandbox = process.env.ORI_ONE_CARD_SANDBOX || '';
const offerID = process.env.ORI_ONE_CARD_REPORT_OFFER_ID || '';
test.skip(!sandbox || !offerID, 'requires an explicit saved sandbox and report offer');
test('saved first report stays readable without re-announcement or another execution', async ({
  page,
  request
}) => {
  await verifySavedFirstLook(page, request, sandbox, offerID);
});
