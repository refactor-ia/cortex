package qapi

// PiSDKVersion is the one Pi build the SDK qualification contract below was
// validated against. It pins qualification only.
//
// No binding and no receipt pins an exact runtime version on any backend any
// more. Three adapters carried two different runtime-trust contracts while
// every receipt read as equally strong, and the asymmetry was the defect, not
// the absence of a pin: each backend's stream is validated structurally at
// parse time, so pinning a build rejects working installations without gaining
// evidence. Qualification is the exception because it inspects the Pi SDK
// surface itself — tool definitions, loaded skills, parser facts — and that
// surface is what one build fixes.
const PiSDKVersion = "0.85.1"
