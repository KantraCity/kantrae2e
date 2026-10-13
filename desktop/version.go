package main

// Set by CI (version_ci.go, see .github/workflows/ci.yml): the release
// version, its build number and the public key update manifests are
// signed with. A local build (appBuild 0 or no key) never updates itself.
var (
	appVersion       = "dev"
	appBuild         = 0
	updatePublicKey  = "" // base64 Ed25519
	updateRepository = "KantraCity/kantrae2e"
)
