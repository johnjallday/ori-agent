import { test } from '@playwright/test';
import { folderHireReportCases } from './helpers/folder-hire-report-cases';

// Runs against two unread, finished, stored reports (one with a suggestion).
// ./scripts/e2e.sh --env ORI_HIRE_REPORTS=1 tests/folder-hire-brief.spec.ts
// An explicit run fails, rather than skipping, when provisioning is missing.
// The software harness also exercises these cases with actual stored reports
// from its labelled scripted provider; that is not live-model acceptance.
test.skip(!process.env.ORI_HIRE_REPORTS, 'requires a deliberately provisioned report sandbox');
test.describe.configure({ mode: 'serial' });
test.setTimeout(120_000);
folderHireReportCases();
