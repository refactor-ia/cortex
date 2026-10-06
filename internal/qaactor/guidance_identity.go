package qaactor

import "errors"

// GuidanceIdentity carries trusted prior identity, not self-authenticating hashes.
// A prior v2 actor uses its full canonical digest and an empty EffectiveSHA256.
// A registered v3 actor requires both canonical and full effective digests.
// Callers must not discard registered effective identity to select legacy mode.
type GuidanceIdentity struct {
	CanonicalSHA256     string
	EffectiveSHA256     string
	AdoptLegacyGuidance bool
}

// ComposeGuidanceWithIdentity proves detached current bytes against prior
// identity before replacing canonical bytes. Legacy guidance requires explicit
// adoption; registered bytes require their full effective digest, including the
// external block. Adoption does not authenticate the block's untrusted content.
//
// Guidance-bearing prior canonical bytes are reconstructed as prefix + ONE LF.
// This is a supported compatibility subset, NOT a universal Render convention.
// Missing or multiple historical trailing LFs fail by canonical digest mismatch;
// no alternate whitespace variants are attempted. Unmarked files use exact bytes
// with no LF restriction. Desired canonical bytes are supplied independently.
//
// This pure helper grants no filesystem, ownership, or admission authority.
// Callers must authenticate prior metadata, validate desired renders, enforce
// root/path/mode and adoption policy, and recheck current bytes when applying
// (TOCTOU). Errors return no replacement and leave all input bytes unchanged.
func ComposeGuidanceWithIdentity(current, desiredCanonical []byte, prior GuidanceIdentity) ([]byte, error) {
	if !isLowerSHA256(prior.CanonicalSHA256) ||
		(prior.EffectiveSHA256 != "" && !isLowerSHA256(prior.EffectiveSHA256)) {
		return nil, errors.New("invalid prior guidance identity digest")
	}
	registered := prior.EffectiveSHA256 != ""
	if registered && !isSHA256Of(prior.EffectiveSHA256, current) {
		return nil, errors.New("current actor differs from registered effective identity")
	}
	prefix, external, err := ParseGuidance(current)
	if err != nil {
		return nil, err
	}
	previousCanonical := prefix
	if external != nil {
		if !registered && !prior.AdoptLegacyGuidance {
			return nil, errors.New("legacy guidance requires explicit adoption")
		}
		previousCanonical = append(previousCanonical, '\n')
		if !isSHA256Of(prior.CanonicalSHA256, previousCanonical) {
			return nil, errors.New("prior canonical identity mismatch: guidance reconstruction supports only one-LF historical endings")
		}
	} else if !isSHA256Of(prior.CanonicalSHA256, previousCanonical) {
		return nil, errors.New("current actor differs from prior canonical identity")
	}
	return ComposeGuidance(current, previousCanonical, desiredCanonical)
}
