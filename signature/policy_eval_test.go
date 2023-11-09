package signature

import (
	"fmt"
	"os"
	"testing"

	"github.com/containers/image/docker/policyconfiguration"
	"github.com/containers/image/types"
	"github.com/docker/docker/reference"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyRequirementError(t *testing.T) {
	// A stupid test just to keep code coverage
	s := "test"
	err := PolicyRequirementError(s)
	assert.Equal(t, s, err.Error())
}

func TestPolicyContextChangeState(t *testing.T) {
	pc, err := NewPolicyContext(&Policy{Default: PolicyRequirements{NewPRReject()}})
	require.NoError(t, err)
	defer pc.Destroy()

	require.Equal(t, pcReady, pc.state)
	err = pc.changeState(pcReady, pcInUse)
	require.NoError(t, err)

	err = pc.changeState(pcReady, pcInUse)
	require.Error(t, err)

	// Return state to pcReady to allow pc.Destroy to clean up.
	err = pc.changeState(pcInUse, pcReady)
	require.NoError(t, err)
}

func TestPolicyContextNewDestroy(t *testing.T) {
	pc, err := NewPolicyContext(&Policy{Default: PolicyRequirements{NewPRReject()}})
	require.NoError(t, err)
	assert.Equal(t, pcReady, pc.state)

	err = pc.Destroy()
	require.NoError(t, err)
	assert.Equal(t, pcDestroyed, pc.state)

	// Trying to destroy when not pcReady
	pc, err = NewPolicyContext(&Policy{Default: PolicyRequirements{NewPRReject()}})
	require.NoError(t, err)
	err = pc.changeState(pcReady, pcInUse)
	require.NoError(t, err)
	err = pc.Destroy()
	require.Error(t, err)
	assert.Equal(t, pcInUse, pc.state) // The state, and hopefully nothing else, has changed.

	err = pc.changeState(pcInUse, pcReady)
	require.NoError(t, err)
	err = pc.Destroy()
	assert.NoError(t, err)
}

// pcImageReferenceMock is a mock of types.ImageReference which returns itself in DockerReference
// and handles PolicyConfigurationIdentity and PolicyConfigurationReference consistently.
type pcImageReferenceMock struct{ ref reference.Named }

func (ref pcImageReferenceMock) Transport() types.ImageTransport {
	// We use this in error messages, so sadly we must return something.
	return nameImageTransportMock("== Transport mock")
}
func (ref pcImageReferenceMock) StringWithinTransport() string {
	// We use this in error messages, so sadly we must return something.
	return "== StringWithinTransport mock"
}
func (ref pcImageReferenceMock) DockerReference() reference.Named {
	return ref.ref
}
func (ref pcImageReferenceMock) PolicyConfigurationIdentity() string {
	if ref.ref == nil {
		return ""
	}
	res, err := policyconfiguration.DockerReferenceIdentity(ref.ref)
	if res == "" || err != nil {
		panic(fmt.Sprintf("Internal inconsistency: policyconfiguration.DockerReferenceIdentity returned %#v, %v", res, err))
	}
	return res
}
func (ref pcImageReferenceMock) PolicyConfigurationNamespaces() []string {
	if ref.ref == nil {
		panic("unexpected call to a mock function")
	}
	return policyconfiguration.DockerReferenceNamespaces(ref.ref)
}
func (ref pcImageReferenceMock) NewImage(certPath string, tlsVerify bool) (types.Image, error) {
	panic("unexpected call to a mock function")
}
func (ref pcImageReferenceMock) NewImageSource(certPath string, tlsVerify bool) (types.ImageSource, error) {
	panic("unexpected call to a mock function")
}
func (ref pcImageReferenceMock) NewImageDestination(certPath string, tlsVerify bool) (types.ImageDestination, error) {
	panic("unexpected call to a mock function")
}

func TestPolicyContextRequirementsForImageRef(t *testing.T) {
	ktGPG := SBKeyTypeGPGKeys
	prm := NewPRMMatchExact()

	policy := &Policy{
		Default:  PolicyRequirements{NewPRReject()},
		Specific: map[string]PolicyRequirements{},
	}
	// Just put _something_ into the Specific map for the keys we care about, and make it pairwise
	// distinct so that we can compare the values and show them when debugging the tests.
	for _, scope := range []string{
		"unmatched",
		"deep.com",
		"deep.com/n1",
		"deep.com/n1/n2",
		"deep.com/n1/n2/n3",
		"deep.com/n1/n2/n3/repo",
		"deep.com/n1/n2/n3/repo:tag2",
	} {
		policy.Specific[scope] = PolicyRequirements{xNewPRSignedByKeyData(ktGPG, []byte(scope), prm)}
	}

	pc, err := NewPolicyContext(policy)
	require.NoError(t, err)

	for input, matched := range map[string]string{
		// Full match
		"deep.com/n1/n2/n3/repo:tag2": "deep.com/n1/n2/n3/repo:tag2",
		// Namespace matches
		"deep.com/n1/n2/n3/repo:nottag2": "deep.com/n1/n2/n3/repo",
		"deep.com/n1/n2/n3/notrepo:tag2": "deep.com/n1/n2/n3",
		"deep.com/n1/n2/notn3/repo:tag2": "deep.com/n1/n2",
		"deep.com/n1/notn2/n3/repo:tag2": "deep.com/n1",
		// Host name match
		"deep.com/notn1/n2/n3/repo:tag2": "deep.com",
		// Default
		"this.doesnt/match:anything": "",
	} {
		var expected PolicyRequirements
		if matched != "" {
			e, ok := policy.Specific[matched]
			require.True(t, ok, fmt.Sprintf("case %s: expected reqs not found", input))
			expected = e
		} else {
			expected = policy.Default
		}

		ref, err := reference.ParseNamed(input)
		require.NoError(t, err)
		reqs, err := pc.requirementsForImageRef(pcImageReferenceMock{ref})
		require.NoError(t, err)
		comment := fmt.Sprintf("case %s: %#v", input, reqs[0])
		// Do not use assert.Equal, which would do a deep contents comparison; we want to compare
		// the pointers. Also, == does not work on slices; so test that the slices start at the
		// same element and have the same length.
		assert.True(t, &(reqs[0]) == &(expected[0]), comment)
		assert.True(t, len(reqs) == len(expected), comment)
	}

	// Image without a Docker reference identity
	_, err = pc.requirementsForImageRef(pcImageReferenceMock{nil})
	assert.Error(t, err)
}

// pcImageMock returns a types.Image for a directory, claiming a specified dockerReference and implementing PolicyConfigurationIdentity/PolicyConfigurationNamespaces.
func pcImageMock(t *testing.T, dir, dockerReference string) types.Image {
	ref, err := reference.ParseNamed(dockerReference)
	require.NoError(t, err)
	return dirImageMockWithRef(t, dir, pcImageReferenceMock{ref})
}

func TestPolicyContextGetSignaturesWithAcceptedAuthor(t *testing.T) {
	expectedSig := &Signature{
		DockerManifestDigest: TestImageManifestDigest,
		DockerReference:      "testing/manifest:latest",
	}

	pc, err := NewPolicyContext(&Policy{
		Default: PolicyRequirements{NewPRReject()},
		Specific: map[string]PolicyRequirements{
			"docker.io/testing/manifest:latest": {
				xNewPRSignedByKeyPath(SBKeyTypeGPGKeys, "fixtures/public-key.gpg", NewPRMMatchExact()),
			},
			"docker.io/testing/manifest:twoAccepts": {
				xNewPRSignedByKeyPath(SBKeyTypeGPGKeys, "fixtures/public-key.gpg", NewPRMMatchRepository()),
				xNewPRSignedByKeyPath(SBKeyTypeGPGKeys, "fixtures/public-key.gpg", NewPRMMatchRepository()),
			},
			"docker.io/testing/manifest:acceptReject": {
				xNewPRSignedByKeyPath(SBKeyTypeGPGKeys, "fixtures/public-key.gpg", NewPRMMatchRepository()),
				NewPRReject(),
			},
			"docker.io/testing/manifest:acceptUnknown": {
				xNewPRSignedByKeyPath(SBKeyTypeGPGKeys, "fixtures/public-key.gpg", NewPRMMatchRepository()),
				xNewPRSignedBaseLayer(NewPRMMatchRepository()),
			},
			"docker.io/testing/manifest:rejectUnknown": {
				NewPRReject(),
				xNewPRSignedBaseLayer(NewPRMMatchRepository()),
			},
			"docker.io/testing/manifest:unknown": {
				xNewPRSignedBaseLayer(NewPRMMatchRepository()),
			},
			"docker.io/testing/manifest:unknown2": {
				NewPRInsecureAcceptAnything(),
			},
			"docker.io/testing/manifest:invalidEmptyRequirements": {},
		},
	})
	require.NoError(t, err)
	defer pc.Destroy()

	// Success
	img := pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:latest")
	sigs, err := pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Equal(t, []*Signature{expectedSig}, sigs)

	// Two signatures
	// FIXME? Use really different signatures for this?
	img = pcImageMock(t, "fixtures/dir-img-valid-2", "testing/manifest:latest")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Equal(t, []*Signature{expectedSig, expectedSig}, sigs)

	// No signatures
	img = pcImageMock(t, "fixtures/dir-img-unsigned", "testing/manifest:latest")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Empty(t, sigs)

	// Only invalid signatures
	img = pcImageMock(t, "fixtures/dir-img-modified-manifest", "testing/manifest:latest")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Empty(t, sigs)

	// 1 invalid, 1 valid signature (in this order)
	img = pcImageMock(t, "fixtures/dir-img-mixed", "testing/manifest:latest")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Equal(t, []*Signature{expectedSig}, sigs)

	// Two sarAccepted results for one signature
	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:twoAccepts")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Equal(t, []*Signature{expectedSig}, sigs)

	// sarAccepted+sarRejected for a signature
	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:acceptReject")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Empty(t, sigs)

	// sarAccepted+sarUnknown for a signature
	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:acceptUnknown")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Equal(t, []*Signature{expectedSig}, sigs)

	// sarRejected+sarUnknown for a signature
	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:rejectUnknown")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Empty(t, sigs)

	// sarUnknown only
	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:unknown")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Empty(t, sigs)

	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:unknown2")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Empty(t, sigs)

	// Empty list of requirements (invalid)
	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:invalidEmptyRequirements")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	require.NoError(t, err)
	assert.Empty(t, sigs)

	// Failures: Make sure we return nil sigs.

	// Unexpected state (context already destroyed)
	destroyedPC, err := NewPolicyContext(pc.Policy)
	require.NoError(t, err)
	err = destroyedPC.Destroy()
	require.NoError(t, err)
	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:latest")
	sigs, err = destroyedPC.GetSignaturesWithAcceptedAuthor(img)
	assert.Error(t, err)
	assert.Nil(t, sigs)
	// Not testing the pcInUse->pcReady transition, that would require custom PolicyRequirement
	// implementations meddling with the state, or threads. This is for catching trivial programmer
	// mistakes only, anyway.

	// Image without a Docker reference identity
	img = dirImageMockWithRef(t, "fixtures/dir-img-valid", pcImageReferenceMock{nil})
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	assert.Error(t, err)
	assert.Nil(t, sigs)

	// Error reading signatures.
	invalidSigDir := createInvalidSigDir(t)
	defer os.RemoveAll(invalidSigDir)
	img = pcImageMock(t, invalidSigDir, "testing/manifest:latest")
	sigs, err = pc.GetSignaturesWithAcceptedAuthor(img)
	assert.Error(t, err)
	assert.Nil(t, sigs)
}

func TestPolicyContextIsRunningImageAllowed(t *testing.T) {
	pc, err := NewPolicyContext(&Policy{
		Default: PolicyRequirements{NewPRReject()},
		Specific: map[string]PolicyRequirements{
			"docker.io/testing/manifest:latest": {
				xNewPRSignedByKeyPath(SBKeyTypeGPGKeys, "fixtures/public-key.gpg", NewPRMMatchExact()),
			},
			"docker.io/testing/manifest:twoAllows": {
				xNewPRSignedByKeyPath(SBKeyTypeGPGKeys, "fixtures/public-key.gpg", NewPRMMatchRepository()),
				xNewPRSignedByKeyPath(SBKeyTypeGPGKeys, "fixtures/public-key.gpg", NewPRMMatchRepository()),
			},
			"docker.io/testing/manifest:allowDeny": {
				xNewPRSignedByKeyPath(SBKeyTypeGPGKeys, "fixtures/public-key.gpg", NewPRMMatchRepository()),
				NewPRReject(),
			},
			"docker.io/testing/manifest:reject": {
				NewPRReject(),
			},
			"docker.io/testing/manifest:acceptAnything": {
				NewPRInsecureAcceptAnything(),
			},
			"docker.io/testing/manifest:invalidEmptyRequirements": {},
		},
	})
	require.NoError(t, err)
	defer pc.Destroy()

	// Success
	img := pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:latest")
	res, err := pc.IsRunningImageAllowed(img)
	assertRunningAllowed(t, res, err)

	// Two signatures
	// FIXME? Use really different signatures for this?
	img = pcImageMock(t, "fixtures/dir-img-valid-2", "testing/manifest:latest")
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningAllowed(t, res, err)

	// No signatures
	img = pcImageMock(t, "fixtures/dir-img-unsigned", "testing/manifest:latest")
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningRejectedPolicyRequirement(t, res, err)

	// Only invalid signatures
	img = pcImageMock(t, "fixtures/dir-img-modified-manifest", "testing/manifest:latest")
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningRejectedPolicyRequirement(t, res, err)

	// 1 invalid, 1 valid signature (in this order)
	img = pcImageMock(t, "fixtures/dir-img-mixed", "testing/manifest:latest")
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningAllowed(t, res, err)

	// Two allowed results
	img = pcImageMock(t, "fixtures/dir-img-mixed", "testing/manifest:twoAllows")
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningAllowed(t, res, err)

	// Allow + deny results
	img = pcImageMock(t, "fixtures/dir-img-mixed", "testing/manifest:allowDeny")
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningRejectedPolicyRequirement(t, res, err)

	// prReject works
	img = pcImageMock(t, "fixtures/dir-img-mixed", "testing/manifest:reject")
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningRejectedPolicyRequirement(t, res, err)

	// prInsecureAcceptAnything works
	img = pcImageMock(t, "fixtures/dir-img-mixed", "testing/manifest:acceptAnything")
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningAllowed(t, res, err)

	// Empty list of requirements (invalid)
	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:invalidEmptyRequirements")
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningRejectedPolicyRequirement(t, res, err)

	// Unexpected state (context already destroyed)
	destroyedPC, err := NewPolicyContext(pc.Policy)
	require.NoError(t, err)
	err = destroyedPC.Destroy()
	require.NoError(t, err)
	img = pcImageMock(t, "fixtures/dir-img-valid", "testing/manifest:latest")
	res, err = destroyedPC.IsRunningImageAllowed(img)
	assertRunningRejected(t, res, err)
	// Not testing the pcInUse->pcReady transition, that would require custom PolicyRequirement
	// implementations meddling with the state, or threads. This is for catching trivial programmer
	// mistakes only, anyway.

	// Image without a Docker reference identity
	img = dirImageMockWithRef(t, "fixtures/dir-img-valid", pcImageReferenceMock{nil})
	res, err = pc.IsRunningImageAllowed(img)
	assertRunningRejected(t, res, err)
}

// Helpers for validating PolicyRequirement.isSignatureAuthorAccepted results:

// assertSARRejected verifies that isSignatureAuthorAccepted returns a consistent sarRejected result
// with the expected signature.
func assertSARAccepted(t *testing.T, sar signatureAcceptanceResult, parsedSig *Signature, err error, expectedSig Signature) {
	assert.Equal(t, sarAccepted, sar)
	assert.Equal(t, &expectedSig, parsedSig)
	assert.NoError(t, err)
}

// assertSARRejected verifies that isSignatureAuthorAccepted returns a consistent sarRejected result.
func assertSARRejected(t *testing.T, sar signatureAcceptanceResult, parsedSig *Signature, err error) {
	assert.Equal(t, sarRejected, sar)
	assert.Nil(t, parsedSig)
	assert.Error(t, err)
}

// assertSARRejectedPolicyRequiremnt verifies that isSignatureAuthorAccepted returns a consistent sarRejected resul,
// and that the returned error is a PolicyRequirementError..
func assertSARRejectedPolicyRequirement(t *testing.T, sar signatureAcceptanceResult, parsedSig *Signature, err error) {
	assertSARRejected(t, sar, parsedSig, err)
	assert.IsType(t, PolicyRequirementError(""), err)
}

// assertSARRejected verifies that isSignatureAuthorAccepted returns a consistent sarUnknown result.
func assertSARUnknown(t *testing.T, sar signatureAcceptanceResult, parsedSig *Signature, err error) {
	assert.Equal(t, sarUnknown, sar)
	assert.Nil(t, parsedSig)
	assert.NoError(t, err)
}

// Helpers for validating PolicyRequirement.isRunningImageAllowed results:

// assertRunningAllowed verifies that isRunningImageAllowed returns a consistent true result
func assertRunningAllowed(t *testing.T, allowed bool, err error) {
	assert.Equal(t, true, allowed)
	assert.NoError(t, err)
}

// assertRunningRejected verifies that isRunningImageAllowed returns a consistent false result
func assertRunningRejected(t *testing.T, allowed bool, err error) {
	assert.Equal(t, false, allowed)
	assert.Error(t, err)
}

// assertRunningRejectedPolicyRequirement verifies that isRunningImageAllowed returns a consistent false result
// and that the returned error is a PolicyRequirementError.
func assertRunningRejectedPolicyRequirement(t *testing.T, allowed bool, err error) {
	assertRunningRejected(t, allowed, err)
	assert.IsType(t, PolicyRequirementError(""), err)
}
