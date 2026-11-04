#!/usr/bin/env bash
# Runs the tests that make up the spec 12 exit criterion for increment 1a, by name.
set -euo pipefail
fail=0
check() {
  local label=$1 pattern=$2 pkg=$3 listed alt
  # go test exits 0 when -run matches nothing, so a misspelt name would pass. Require every
  # alternative in the pattern to match the whole name of at least one test that exists: an
  # alternative is anchored at both ends, so a deleted test whose name is a prefix of a surviving
  # one is missing, not present. A family of tests is written as an explicit regex (TestRule.*).
  listed=$(go test -list . "$pkg" 2>/dev/null | grep '^Test' || true)
  local IFS='|'
  for alt in $pattern; do
    if ! grep -Eqx -- "$alt" <<<"$listed"; then
      printf 'FAIL  %s  (no test matches %q in %s)\n' "$label" "$alt" "$pkg"
      fail=1
      return 0
    fi
  done
  IFS=$' \t\n'
  if go test -count=1 -run "^($pattern)\$" "$pkg" >/dev/null 2>&1; then
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
check "turn slices bounded"                'TestTurnSliceStopsAtTheNextTurn|TestRepeatedFinalText.*|TestDelayedFlush.*|TestSealDoesNotCountAnAdoptedCapturedVersion|TestRepairedOrphanStubIsCompletedByItsStop' ./internal/capture/
check "every persisted field redacted"     'TestRequestAndFinalAreRedacted|TestSealedRecordRedactsCommandsAndVerification|TestMetadataIsRedactedAndNoAbsolutePathIsStored' ./internal/capture/
check "duplicate Stop through the hook"    'TestHookStopCapturesAndDuplicateIsIdempotent' ./internal/app/
check "interruption before the first Stop" 'TestInterruptionBeforeFirstStop|TestSessionStartStubsStaleMarkersOfOtherSessions' ./internal/capture/
check "interruption disclosed in briefing" 'TestHookInterruptionBeforeFirstStopIsDisclosed' ./internal/app/
check "malformed input"                    'TestDecodeRejectsMalformed' ./internal/decode/claude/
check "malformed input through the hook"   'TestHookMalformedInputExitsZeroAndLogsOnlyTheKind' ./internal/app/
check "stale or switched checkout"         'TestHookCopiedCheckoutGetsNoPrivateData|TestHookMovedCheckoutIsUnregistered|TestHookNewWorktreeOfTrustedCloneIsRegistered' ./internal/app/
check "branch switch scope"                'TestSearchAfterBranchSwitch|TestSearchScopeIsThisCheckoutOnly' ./internal/retrieve/
check "checkout binding of publish"        'TestWrongCheckoutRefused|TestRecoveryNeverAppliesAnotherCheckoutsJob' ./internal/publish/
check "symlink and path escape (confine)"  'TestRefuses.*|TestDirectorySwapAfterWalkCannotRedirect|TestComponentSwapBetweenCheckAndOpenIsRefused|TestFileSwapBetweenCheckAndOpenIsRefused|TestOpenRefusesSymlinkedRoot|TestRootSwapDuringOpenIsRefused|TestRootHandleSurvivesReplacementAfterOpen|TestWriteAtomicRefusesSymlinkTarget|Test.*SubstitutedBySymlinkToMovedOriginalIsRefused' ./internal/confine/
check "human edits never overwritten"     'TestReplace.*|TestRestoreBackupsPutsBackAMissingTarget' ./internal/confine/
check "human save after a killed replace"  'TestHumanSaveAfterAKilledMoveAsideKeepsBothVersions' ./internal/publish/
check "hostile git configuration"          'TestRepositoryFsmonitorIsNeverRun|TestRepositoryCleanFilterIsNeverRun|TestAmbiguousShortIDIsAnError' ./internal/gitx/
check "symlink and path escape (readers)"  'TestRefreshRefusesSymlinkedPagesAndDirs|TestGitEnvWorkBlobIsConfined|TestSymlinkedTranscriptIsRefused|TestInitRefusesSymlinkedProjectMemory' ./...
check "kill at each publish step"          'TestKillAtEachStepThenRecover|TestHumanEditAfterPartialPublishIsNeverOverwritten|TestHumanEditOfAnAlreadyAppliedPageIsAConflict' ./internal/publish/
check "recovery fingerprint and fencing"   'TestRecoveryRefusesAfterABranchSwitch|TestRecoveryFencesTheOldHolder|TestTakeoverWaitsForThePublishingLock|TestEditBetweenStageAndApplyIsAConflict|TestOpsAreMarkedInTheDatabase' ./internal/publish/
check "owner-only permissions"             'TestStoreIsOwnerOnlyUnderPermissiveUmask|TestEnsurePrivateDir.*' ./internal/store/
check "validator rules"                    'TestRule.*|TestShortEvidenceIsResolvedAndAmbiguityIsAnError|TestDeriveNeverConfirmsAShortID' ./internal/validate/
check "stamp keeps dirty evidence unverified" 'TestStamp.*' ./internal/app/
check "sanitiser"                          'TestSanitize.*|TestSearchSanitisesFileNames' ./internal/retrieve/
check "latency budgets"                    'TestSessionStartWithinTwoSecondsOnAFixtureWiki|TestStopCaptureWithinBudget|TestHookDegradedOnTimeout|TestHookTimeoutDoesNoSynchronousWork|TestFIFOAtHookErrorLogDoesNotStallSessionStart|TestStalledStdinDoesNotStallSessionStart' ./internal/app/
check "degraded on a corrupt database"     'TestHookUnopenableStoreGetsStatusOnlyNeverRepoContent' ./internal/app/
# Stage 2: bootstrap (design 2026-11-02).
check "bootstrap tables purged with the clone"  'TestEveryCheckoutKeyedTableIsInThePurgeList|TestRebindPurgesBootstrapRowsAndWorkFiles' ./internal/store/
check "git inventory is confined"               'TestListTreeAcceptsOnlyHeadOrAHexCommitID|TestReadBlobHonoursTheLimitAndRefusesNonIDs|TestListTreeReturnsRegularBlobsWithSizes' ./internal/gitx/
check "secret scan sees what redaction changes" 'TestScanSeesEverythingStringRedacts|TestScanLeavesMarkersComparisonsAndProseAlone' ./internal/redact/
check "sources: exclusions, links, bounds"      'TestExclusionReasons|TestDirReaderWalksThroughTheConfinedRootAndNeverFollowsLinks|TestDirReaderBoundsDepthAndEntryCount' ./internal/bootstrap/
check "inventory never keeps secret text"       'TestInventoryDisposesEachSourceAndNeverKeepsSecretText|TestInventoryOutsideSourcesAreAttributedAndNeverComponents' ./internal/bootstrap/
check "plan hash covers what would be done"     'TestPlanHashIsStableAndSensitive|TestComponentDigestSpecIsBoundedAndHashesItsFiles' ./internal/bootstrap/
check "leases: epochs, expiry, atomic submit"   'TestAnExpiredLeaseIsReclaimedAndAStaleCompleteIsRefused|TestCompleteIsAtomicWithTheCallersWrites|TestEveryMutationTakesTheWriteLock|TestARunOfAnotherCheckoutIsInvisible|TestNextRefusesARunThatIsNotConfirmed|TestSamplingLeasesOnlyTheSampledItemsAndThenAwaitsConfirmation' ./internal/bootstrap/
check "work files redacted, hex-named, private" 'TestWorkFileIsRedactedPrivateAndReadable|TestWorkFileNamesAreHexOnly|TestAFileCannotForgeAnotherFilesHeader|TestPrepareWorkRefusesAFileThatChangedSinceInventory' ./internal/bootstrap/
check "excerpts are verified verbatim"          'TestCheckProposalRejections|TestCheckProposalExcerptMatchingIsCanonicalButLocatorsKeepFileLines' ./internal/bootstrap/
check "extracted claims never exceed their cap" 'TestAdmitNeverReturnsAVerifiedClaim|TestCapStatus|TestAnInjectionInASourceCannotRaiseAStatusOrForgeAClaim|TestSubmitRefusesAStaleEpochAWrongBatchAndAWrongStage' ./internal/bootstrap/
check "verified only from code evidence"        'TestConfirmedBecomesVerifiedWithCodeEvidence|TestConfirmedWithoutResolvableCodeEvidenceStaysUnverified|TestVerdictsAreCheckedBeforeAnythingChanges|TestAClaimAlreadyDecidedIsNotDecidedAgain' ./internal/bootstrap/
check "contradiction keeps both claims"         'TestContradictedYieldsTwoConflictingClaimsLinkedBothWays' ./internal/bootstrap/
check "pages: placement, splitting, empties"    'TestEveryClaimIsRenderedOnExactlyOnePageAndParsesBack|TestClaimsLandWhereTheRulesSendThem|TestAPageOverTheBudgetSplitsIntoNumberedPagesKeepingHeadings|TestEmptySectionsSayNothingWasFoundInsteadOfStayingBlank' ./internal/bootstrap/
check "human-edited pages never replaced (plan)" 'TestPlanOpsCreatesUnchangedAndConflictRules' ./internal/bootstrap/
check "human-edited pages never replaced (finish)" 'TestFinishNeverOverwritesAHumanEditedPage' ./internal/app/
check "coverage names skips, never text"        'TestCoverageReportsReadSkippedSecretsAndRetirement|TestCoverageNeverContainsSecretTextOrClaimText' ./internal/bootstrap/
check "finish: complete, current, searchable"   'TestFinishPublishesPagesAndTheyAreSearchable|TestFinishRefusesWhenTheRepositoryMovedSinceThePlan' ./internal/app/
check "finish survives a real process kill"     'TestFinishSurvivesAProcessKillAtEveryBoundary|TestKillClassifierAcceptsOnlyARealSIGKILL' ./internal/app/
check "kill inside a replace leaves no residue" 'TestASecondBootstrapFinishSurvivesAKillInsideAReplaceOfAnEarlierPage' ./internal/app/
check "cost gate needs the plan hash"           'TestConfirmNeedsTheExactPlanHash|TestASampledRunNeedsTheSampleConfirmationFirst' ./internal/app/
check "the sample is measured"                  'TestMeasureReportsDurationTokensAcceptanceAndAProjection' ./internal/bootstrap/
check "plan prints no secret and refuses dirt"  'TestBootstrapPlanNeverPrintsSecretText|TestBootstrapPlanRefusesAFeatureBranchAndADirtyTree' ./internal/app/
check "plugin agents are least privilege"       'TestExtractorAndVerifierToolListsAreMinimal|TestBootstrapCommandKeepsItsSafetyRules' ./internal/app/
check "hostile source text stays data"          'TestHostileSourceTextBecomesDataAndStaysUnverified' ./internal/app/
check "progress: tty, stages, estimates"        'TestIsTTYIsFalseForBuffersAndTheNullDevice|TestProgressShowsStageNameAndAnEstimatedTimeLeft|TestProgressJSONCarriesStageAndLabelsTheEstimate' ./internal/app/
check "origin frontmatter parses"               'TestPageOriginIsParsedAndNoLongerFlagged' ./internal/claim/
check "model output is scanned, nothing echoed" 'TestCheckTextRefusesASecretAndNeverQuotesIt|TestSubmitRefusesAClaimThatCarriesASecretAndEchoesNothing|TestSubmitDoesNotRepeatAnItemIDItWasGiven|TestAContradictionCannotCarryASecretIntoTheWiki|TestAHostileEvidencePathIsNeverEchoed' ./internal/bootstrap/
check "submit never echoes subagent text"       'TestSubmitNeverEchoesHostileTextFromTheSubagent' ./internal/app/
check "one verifier per claim, no overwrite"    'TestAClaimNamingTwoComponentsGoesToExactlyOneVerifier|TestASharedClaimIsQueuedForOneVerifierAndTheOtherCannotJudgeIt|TestAClaimDecidedUnderneathIsNotOverwritten|TestAVerifyItemIsQueuedOncePerComponent|TestRacingPrepareVerifyCallsQueueOneItemPerComponent' ./internal/bootstrap/
check "the sample settles and its figures hold" 'TestASampleWhoseLastItemIsRejectedStillSettles|TestASampleWhoseLastLeaseExpiresStillSettles|TestAnEmptySampleSettlesAtOnce|TestMeasureStaysTheSampleAfterTheFullRunStarts' ./internal/bootstrap/
check "an item ref is unique per run and stage" 'TestAnItemRefIsUniquePerRunAndStage' ./internal/store/
check "git: dates, branches, worktrees"        'TestLastChangedIsTheNewestCommitTimePerPath|TestBranchesListsOnlyLocalBranchesWithCommitsHeadLacks|TestWorktreesListsTheOtherWorkingTreesNotThisOne' ./internal/gitx/
check "nothing a reader drops is silent"        'TestDirReaderRecordsWhatItDrops|TestInventoryRecordsWhatADirectoryReaderDroppedInsteadOfLosingIt|TestUnreadSourcesNameBranchesAndWorktreesAndInventoryNeverReadsThem|TestInventoryDatesRepositoryFilesByCommitTimeAndDirectoryFilesByModificationTime|TestSourceDatesSurviveTheRunStoreAndStayOutOfThePlanHash' ./internal/bootstrap/
check "coverage names what was not read"        'TestCoverageNamesWhatWasNeverOpenedAndWhenSourcesLastChanged|TestCoverageCountsImplementationFilesThatWereScannedButNeverSummarised|TestCoverageCountsClaimsByKindAndStatus' ./internal/bootstrap/
check "plan lists branches, worktrees, drops"   'TestBootstrapPlanListsUnmergedBranchesAndOtherWorktreesAsNotRead|TestBootstrapPlanCountsWhatASourceDirectoryDropped' ./internal/app/
check "every page fits the parse cap"           'TestEveryRenderedPageFitsTheParseCapWhateverTheComponentCount|TestEverySplitPageIsWithinTheBudgetIncludingItsHeader' ./internal/bootstrap/
check "rendered pages are scanned (scan)"       'TestScanPagesNamesThePageAndKindNeverTheText' ./internal/bootstrap/
check "rendered pages are scanned (finish)"     'TestFinishRefusesToPublishAPageThatWouldHoldASecret' ./internal/app/
check "next and finish show progress"           'TestNextAndFinishShowProgressForTheirLongSteps' ./internal/app/

# Stage 2: safety tests added by the review fix rounds, grouped by what they guard.
# Secrets never reach work files, pages, refusals or the coverage report.
check "secrets: work files and digests (bootstrap)" 'TestWriteDigestRedactsEachFilesTextAndKeepsEverySection|TestTheWorkDirectoryIsPrivate' ./internal/bootstrap/
check "secrets: work files and digests (app)" 'TestBootstrapNextHandsOutRedactedWorkFilesAndNeverTheSameItemTwice' ./internal/app/
check "secrets: validator refusals redacted" 'TestSubmitRedactsASecretShapeInAValidatorRefusal|TestCoverageNeverPrintsAPathThatCarriesAControlCharacterOrASecretShape|TestInventoryRecordsHostileAndSecretShapedNamesWithoutReadingThem|TestScanPagesSeesSecretShapedTextOutsideClaims' ./internal/bootstrap/
check "secrets: a page that would hold one" 'TestFinishWithASecretOnAFixedPageRecordsAndWritesNothing|TestFinishRefusalNamesAComponentPageOnlyByItsHashAndWritesNothing' ./internal/app/
# Hostile file names, paths and git output.
check "hostile names: digests and exclusions" 'TestAHostileFileNameCannotForgeAHeaderOrTheOmittedMark|TestExclusionLooksOnlyAtTheHeadOfTheFile|TestAdmitRefusesAnEscapingEvidencePath|TestDirReaderReadRefusesWhatAListedFileBecame|TestDirReaderRecordsDirectoriesWithNamesTheRootCannotAddress' ./internal/bootstrap/
check "hostile names: git output and worktrees" 'TestLastChangedCannotBeSpoofedByAFileNameThatLooksLikeAHeader|TestLastChangedSurvivesAControlCharacterAtTheStartOfAFileName|TestWorktreesIgnoresAPathForgedThroughAGitdirFile|TestUsableWorktreeRefusesRelativeAndControlCharacterPaths|TestWorktreesLeavesOutALockedWorktreeWhoseDirectoryIsGone' ./internal/gitx/
check "hostile names: page file names" 'TestAVeryDeepPrefixStillGivesAUsableFileName|TestAComponentNameWithMarkupStillRendersPagesTheParserReadsWhole|TestEveryRenderedPageGetsALabelThatNeverRepeatsAComponentName|TestPageLabelNamesAFixedPageAndOnlyTheHashOfAComponentPage' ./internal/bootstrap/
# Verification: one owner per claim, compare-and-swap writes, errors are not verdicts.
check "verify: a refusal changes nothing" 'TestARefusedConfirmationLeavesAHistoricalClaimByteIdentical|TestARefusedConfirmationLeavesAnUnverifiedClaimByteIdentical|TestAContradictionWithUnresolvableEvidenceIsRefusedAndWritesNothing|TestVerifyStaleEpochAndWrongStageAreRefused' ./internal/bootstrap/
check "verify: infrastructure faults are errors" 'TestABlobReadFailureIsAnErrorNotAReject|TestACorruptedStoredClaimIsAnErrorNotAReject|TestAnInfrastructureFailureLeavesTheItemLeasedAndUnmodified|TestATypedValidatorRefusalIsARejectNotAnError|TestIsRefusalSeparatesVerdictsFromInfrastructureFailures' ./internal/bootstrap/
check "verify: ownership and scope of a verdict" 'TestEnqueueVerifyReportsHowManyItemsItQueued|TestAVerifierNamingAnotherComponentsDecidedClaimIsToldItIsUnknown|TestAClaimNamingOnlyAnUninspectedComponentIsAssignedToNobody|TestEvidenceInTheComponentsOwnDirectoryMustStillBeQueuedCode|TestAClaimMovedRequeueReportsNoRejectsIgnoredOrCounts|TestPrepareVerifyDoesNothingWhileAnExtractItemIsOpen|TestEnqueueVerifyInsertsNothingIntoARunThatIsNotRunning' ./internal/bootstrap/
# Cost gate: nothing paid before the exact plan hash, sample and full run each confirmed.
check "cost gate: hash rebuilt, repository held" 'TestConfirmRecomputesThePlanHashFromTheStoredRun|TestConfirmRefusesARunWhoseRepositoryHasMoved|TestAMistypedHashOnAnUntouchedStorePointsAtWhereTheHashIsPrinted|TestConfirmAcceptsARunPlannedWithSourcesWhoseNamesSortAwkwardly' ./internal/app/
check "cost gate: the sample is frozen" 'TestASampledRunReachesTheFullRunAndTheSampleFiguresStayFrozen|TestARefusedFullConfirmLeavesASampledRunUnconfirmed|TestConfirmSampleOnARunWithoutASampleIsAUsageError|TestConfirmSampleTakesNoValue|TestBootstrapPlanBindsTheSampleSizeToTheRun|TestBootstrapNextRefusesAnUnconfirmedRunAndStatusReportsIt' ./internal/app/
check "cost gate: plan hash and run store (bootstrap)" 'TestPlanHashCoversSourceTier|TestPlanHashCoversTheWorkListEstimatesAndBatches|TestPlanHashDoesNotDependOnWhereAWorktreeLives' ./internal/bootstrap/
check "cost gate: plan hash and run store (app)" 'TestBootstrapPlanScopeAndHashDoNotDependOnSourceFlagOrder|TestBootstrapPlanRefusesTheSameSourceNameTwice' ./internal/app/
check "cost gate: sample figures stay frozen" 'TestTheFrozenSampleFiguresIncludeTheSettlingFailureAndStayFrozen|TestARunPlannedWithoutASampleRefusesToStartOne|TestTheRunStoresTheSampleSizeAndFixesItsSampledItemsAtCreate|TestAnItemThatKeepsExpiringIsRejectedAfterMaxAttempts|TestAnItemLeasedBeforeTheRunLeftRunningCannotCommitLater' ./internal/bootstrap/
check "cost gate: the command pins the sequence" 'TestBootstrapCommandPreApprovesEachListedSubcommandAndNeverConfirm|TestBootstrapCommandNamesTheConfirmPromptAsTheGateAndDefinesYes|TestBootstrapCommandPinsTheSampleSequenceAndTheLoopExit|TestTheSampleSequenceInTheCommandIsTheSequenceTheBinaryAllows|TestBootstrapCommandKeepsTheDriverOutOfWorkFilesAndBehindTheGate' ./internal/app/
# Submit: decode everything before storing, bound the usage, echo nothing the subagent chose.
check "submit: whole batch decoded first" 'TestSubmitDecodesTheWholeBatchBeforeStoringAnything|TestSubmitRefusesDataAfterTheJSONObject|TestSubmitRefusesAnItemThatBelongsToAnotherRun|TestAClaimCarryingASmuggledFieldRefusesTheWholeSubmissionAndRecordsNothing|TestVerifySubmitDecodesTheWholeBatchBeforeApplyingAnything|TestVerifySubmitRefusesAnItemThatBelongsToAnotherRun' ./internal/app/
check "submit: usage must be real" 'TestSubmitRefusesUsageThatCannotBeRealAndStoresNothing|TestVerifySubmitRefusesUsageThatCannotBeRealAndAppliesNothing' ./internal/app/
check "submit: usage bounds (store layer)" 'TestSubmitRefusesUsageThatIsNegativeOrAbsurdBeforeStoringAnything|TestUsageCheckAcceptsTheBoundsAndNothingBeyond|TestTheBadUsageMessageNamesTheBound|TestVerifyRefusesUsageThatCannotBeRealBeforeAnythingChanges|TestARequeuedVerifySubmissionCarriesItsUsage|TestARequeueAfterAClaimMovedAlsoCarriesItsUsage' ./internal/bootstrap/
check "submit: nothing echoed, nothing lost" 'TestVerifySubmitNeverEchoesHostileTextFromTheVerifier|TestVerifySubmitForAnUnknownItemReportsNoItemId' ./internal/app/
check "submit: failures report only what held" 'TestSubmitReturnsAnInfrastructureFailureAndChangesNothing|TestSubmitReportsNothingWhenTheCompletionFails|TestVerifyReportNamesTheItemOnlyAfterTheStoreFoundIt|TestAFailedCompletionReportsNothingItDidNotWrite' ./internal/bootstrap/
check "submit: late work is refused" 'TestASubmissionAfterFinishIsRefusedAndStoresNothing' ./internal/app/
# Finish: re-check after entering finishing, refuse late work, record what is on disk.
check "finish: preconditions re-checked" 'TestFinishDoesNotPublishWhenTheLastExtractLandsAfterThePrepareCheck|TestARefusalAfterEnteringFinishingLeavesTheRunRunning|TestARefusalLeavesAFinishingRunFinishingWhenThisCallDidNotMoveIt|TestFinishRefusalNamesBothStatesThatCanBeFinished' ./internal/app/
check "finish: the state moves exactly once" 'TestEnterFinishingReportsWhetherThisCallMadeTheMove|TestFinishSetsTheEndMarkerOnlyFromRunningOrFinishing' ./internal/bootstrap/
check "finish: conflicts and oversize pages" 'TestFinishExplainsAPageEditedAfterPlanningAndAFreshFinishReportsItAsAConflict|TestFinishTreatsAnOversizePageAsAConflictNotAnAbort|TestDescribePublishConflictTellsARefusedJobFromAnAppliedOne' ./internal/app/
check "finish: records the bytes on disk" 'TestFinishRecordsTheHashOfTheBytesOnDiskAndRerunsAsUnchanged|TestFinishPublishesInSeveralJobsWhenThePagesDoNotFitOne|TestFinishListsPagesAnEarlierBootstrapWroteThatItDidNotRenderThisTime' ./internal/app/
check "finish: recorded hashes and read errors" 'TestPublishedHashesAreRecordedPerCheckout|TestPlanOpsPropagatesAReadError' ./internal/bootstrap/
# Pages are rendered so that the real parser reads every claim.
check "pages: the parser reads every claim" 'TestPagesTheParserWouldRefuseForStructureAreSplitUntilItReadsEveryClaim|TestAClaimTheParserCannotReadEvenAloneIsPlacedAloneAndReported|TestPartsAreFilledToTheByteBudgetBeforeTheParserIsAsked|TestEachPartOfASplitPageLinksOnlyToItsFirstPreviousAndNextPart' ./internal/bootstrap/
check "pages: rendering is deterministic" 'TestPagesAreDeterministicAndBannerEveryPage|TestRenderingTwiceGivesTheSamePagesWhateverOrderTheClaimsArriveIn|TestComponentsAreRankedHoweverTheCallerOrderedThem|TestAClaimAboutAnUninspectedComponentFallsBackToTheGeneralPage' ./internal/bootstrap/
check "pages: unreadable claims are reported (app)" 'TestFinishReportsAClaimThePageHoldsButTheIndexCannotRead' ./internal/app/
check "pages: unreadable claims are reported (bootstrap)" 'TestCoverageNamesClaimsThePageHoldsButTheIndexCannotRead' ./internal/bootstrap/
# Sample, leases and extraction limits.
check "the sample is spread and exact" 'TestSampleSetIsSpreadAndExact|TestMeasureOfARunWithoutASampleIsZero|TestMeasureIsLiveWhileTheSampleRuns|TestFailCountedAddsUsageAndCountsToTheirOwnColumns' ./internal/bootstrap/
check "extraction: caps, duplicates, markers" 'TestCheckProposalDuplicatesAndTheCountCap|TestCheckTextStillAcceptsProseThatOnlyLooksLikeAMarker|TestSubmitTiesAComponentClaimToTheFileItsLocatorLandsIn|TestSubmitWithNoUsableClaimRequeuesTheItem|TestSubmitCountsATypedValidatorRefusalAsAReject' ./internal/bootstrap/
check "work files: complete and hex-named (bootstrap)" 'TestPrepareWorkKeepsEverySectionWhenAFileEndsOnAnUnsetAssignment|TestWritingTheSameWorkFileTwiceIsFine' ./internal/bootstrap/
check "work files: complete and hex-named (app)" 'TestBootstrapNextNamesAnItemWhoseWorkFileIsMissing' ./internal/app/
check "inventory: bounded, cancellable, honest" 'TestInventoryStopsOnACancelledContextInsteadOfReturningAResult|TestInventoryMapsAnOversizeReadErrorToTheOversizeDisposition|TestDirReaderMaxEntriesBoundary|TestCountMentionsNamesAComponentOnlyAtAPathBoundary|TestInventoryAttributesChurnOnlyToQueuedImplementationSources' ./internal/bootstrap/
check "coverage: honest about work given up (bootstrap)" 'TestCoverageSaysHowMuchWorkWasGivenUpOn|TestCoverageLabelsProposalsAsExtractAdmissionsOnly|TestCoverageListsPagesAnEarlierBootstrapWroteThatWereNotRenderedThisTime' ./internal/bootstrap/
check "coverage: honest about work given up (app)" 'TestStatusAndFinishSayThatAcceptedAndRejectedCountExtractSubmissionsOnly|TestStatusShowsTheMeasuredSampleAndLabelsTokensAsAsserted' ./internal/app/
# Plugin: least privilege and the exact words of the gate.
check "plugin: tool lists and frontmatter" 'TestAgentFrontmatterGrantsNothingBeyondNameDescriptionAndTools|TestPluginTextStatesTheRestrictionsAsTheyAre' ./internal/app/
check "plugin: commands and shapes are real" 'TestBootstrapCommandNamesOnlyRealSubcommandsAndFlags|TestAgentOutputShapesDecodeWithTheBinarysStrictDecoder|TestBootstrapCommandTellsTheDriverHowToHandleUsageAndSubmitInput' ./internal/app/
check "plugin: a paid run waits for V-bootstrap-1" 'TestReadmeBlocksAPaidBootstrapUntilTheReadPermissionCheckIsRecorded|TestReadmeListsTheManualChecksTheCommandDependsOn' ./internal/app/
# Progress: no stale text, one envelope on stdout.
check "progress: clean lines and envelopes" 'TestProgressGivesNoEstimateBeforeAnyStep|TestProgressTTYLineLeavesNoStaleTextWhenTheEstimateShrinks|TestProgressGivesNoEstimateOnceEverythingIsDone|TestProgressBeginResetsTheRememberedLineWidth|TestProgressBeginClearsTheStageFields|TestProgressJSONLinesStayParseableOnATerminal' ./internal/app/
check "progress: events on stderr only" 'TestProgressEventsOnStderrAreOneJSONObjectPerLineAndStdoutStaysOneEnvelope|TestAFailedFinishEndsTheProgressLineOnATerminal' ./internal/app/
# Plan and git inventory.
check "plan: trusted checkout, new run per plan" 'TestBootstrapNeedsATrustedRegisteredCheckout|TestBootstrapPlanIsDeterministicButMakesANewRun|TestBootstrapPlanReportsAndStoresARun|TestBootstrapUsage' ./internal/app/
check "git inventory: churn and default branch" 'TestChurnCountsCommitsPerPathIncludingAwkwardNames|TestDefaultBranchFallsBackToMainThenMaster' ./internal/gitx/
check "scan: spans are exact and ordered" 'TestScanReportsOffsetsOfTheSecretOnly|TestScanReportsEachPlaceOnce|TestScanSpansAreOrderedAndInRange|TestScanUnionsAPartiallyOverlappingTail' ./internal/redact/
check "cost gate: the estimates shown are right"  'TestEstimateStagesCountsItemsAndTokens|TestEstimateStagesVerifyTokensScaleWithInspectedComponents|TestBatchCountRoundsUpAtTheBatchBoundary' ./internal/bootstrap/
exit $fail
