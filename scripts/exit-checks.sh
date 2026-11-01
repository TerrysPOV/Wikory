#!/usr/bin/env bash
# Runs the tests that make up the spec 12 exit criterion for increment 1a, by name.
set -euo pipefail
fail=0
check() {
  local label=$1 pattern=$2 pkg=$3 listed alt
  # go test exits 0 when -run matches nothing, so a misspelt name would pass. Require every
  # alternative in the pattern to match at least one test that exists.
  listed=$(go test -list . "$pkg" 2>/dev/null | grep '^Test' || true)
  local IFS='|'
  for alt in $pattern; do
    if ! grep -Eq -- "$alt" <<<"$listed"; then
      printf 'FAIL  %s  (no test matches %q in %s)\n' "$label" "$alt" "$pkg"
      fail=1
      return 0
    fi
  done
  IFS=$' \t\n'
  if go test -count=1 -run "$pattern" "$pkg" >/dev/null 2>&1; then
    printf 'PASS  %s\n' "$label"
  else
    printf 'FAIL  %s  (go test -run %q %s)\n' "$label" "$pattern" "$pkg"
    fail=1
  fi
}
scripts/inject_selftest.sh >/dev/null 2>&1 && echo "PASS  inject.sh reports compile errors and panics as INCONCLUSIVE" || { echo "FAIL  scripts/inject_selftest.sh"; fail=1; }
scripts/scrub_selftest.sh >/dev/null 2>&1 && echo "PASS  scrub-fixture.sh refuses residue without printing it" || { echo "FAIL  scripts/scrub_selftest.sh"; fail=1; }
check "duplicate Stop events"              'TestDuplicateStopNeverOverwrites|TestStopWithoutPromptIDUsesStoredMarker' ./internal/capture/
check "capture versions serialised"        'TestStaleTransitionNeverRewindsVersion|TestOrphanedVersionFileIsAdopted|TestConcurrentSealAndStubCompletionAgree' ./internal/capture/
check "turn slices bounded"                'TestTurnSliceStopsAtTheNextTurn|TestRepeatedFinalText|TestDelayedFlush|TestSealDoesNotCountAnAdoptedCapturedVersion|TestRepairedOrphanStubIsCompletedByItsStop' ./internal/capture/
check "every persisted field redacted"     'TestRequestAndFinalAreRedacted|TestSealedRecordRedactsCommandsAndVerification|TestMetadataIsRedactedAndNoAbsolutePathIsStored' ./internal/capture/
check "duplicate Stop through the hook"    'TestHookStopCapturesAndDuplicateIsIdempotent' ./internal/app/
check "interruption before the first Stop" 'TestInterruptionBeforeFirstStop|TestSessionStartStubsStaleMarkersOfOtherSessions' ./internal/capture/
check "interruption disclosed in briefing" 'TestHookInterruptionBeforeFirstStopIsDisclosed' ./internal/app/
check "malformed input"                    'TestDecodeRejectsMalformed' ./internal/decode/claude/
check "malformed input through the hook"   'TestHookMalformedInputExitsZeroAndLogsOnlyTheKind' ./internal/app/
check "stale or switched checkout"         'TestHookCopiedCheckoutGetsNoPrivateData|TestHookMovedCheckoutIsUnregistered|TestHookNewWorktreeOfTrustedCloneIsRegistered' ./internal/app/
check "branch switch scope"                'TestSearchAfterBranchSwitch|TestSearchScopeIsThisCheckoutOnly' ./internal/retrieve/
check "checkout binding of publish"        'TestWrongCheckoutRefused|TestRecoveryNeverAppliesAnotherCheckoutsJob' ./internal/publish/
check "symlink and path escape (confine)"  'TestRefuses|TestDirectorySwapAfterWalkCannotRedirect|TestComponentSwapBetweenCheckAndOpenIsRefused|TestFileSwapBetweenCheckAndOpenIsRefused|TestOpenRefusesSymlinkedRoot|TestRootSwapDuringOpenIsRefused|TestRootHandleSurvivesReplacementAfterOpen|TestWriteAtomicRefusesSymlinkTarget|SubstitutedBySymlinkToMovedOriginalIsRefused' ./internal/confine/
check "human edits never overwritten"     'TestReplace|TestRestoreBackupsPutsBackAMissingTarget' ./internal/confine/
check "human save after a killed replace"  'TestHumanSaveAfterAKilledMoveAsideKeepsBothVersions' ./internal/publish/
check "hostile git configuration"          'TestRepositoryFsmonitorIsNeverRun|TestRepositoryCleanFilterIsNeverRun|TestAmbiguousShortIDIsAnError' ./internal/gitx/
check "symlink and path escape (readers)"  'TestRefreshRefusesSymlinkedPagesAndDirs|TestGitEnvWorkBlobIsConfined|TestSymlinkedTranscriptIsRefused|TestInitRefusesSymlinkedProjectMemory' ./...
check "kill at each publish step"          'TestKillAtEachStepThenRecover|TestHumanEditAfterPartialPublishIsNeverOverwritten|TestHumanEditOfAnAlreadyAppliedPageIsAConflict' ./internal/publish/
check "recovery fingerprint and fencing"   'TestRecoveryRefusesAfterABranchSwitch|TestRecoveryFencesTheOldHolder|TestTakeoverWaitsForThePublishingLock|TestEditBetweenStageAndApplyIsAConflict|TestOpsAreMarkedInTheDatabase' ./internal/publish/
check "owner-only permissions"             'TestStoreIsOwnerOnlyUnderPermissiveUmask|TestEnsurePrivateDir' ./internal/store/
check "validator rules"                    'TestRule|TestShortEvidenceIsResolvedAndAmbiguityIsAnError|TestDeriveNeverConfirmsAShortID' ./internal/validate/
check "stamp keeps dirty evidence unverified" 'TestStamp' ./internal/app/
check "sanitiser"                          'TestSanitize|TestSearchSanitisesFileNames' ./internal/retrieve/
check "latency budgets"                    'TestSessionStartWithinTwoSecondsOnAFixtureWiki|TestStopCaptureWithinBudget|TestHookDegradedOnTimeout|TestHookTimeoutDoesNoSynchronousWork|TestFIFOAtHookErrorLogDoesNotStallSessionStart|TestStalledStdinDoesNotStallSessionStart' ./internal/app/
check "degraded on a corrupt database"     'TestHookUnopenableStoreGetsStatusOnlyNeverRepoContent' ./internal/app/
exit $fail
