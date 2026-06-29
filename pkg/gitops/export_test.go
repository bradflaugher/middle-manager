package gitops

// Test-only re-exports of unexported helpers so external _test packages can
// exercise pure logic without shelling out to gh.
var RequiredBucketStateForTest = requiredBucketState
