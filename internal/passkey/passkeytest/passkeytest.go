// Package passkeytest is a software WebAuthn authenticator for tests. It
// answers a registration the way a browser's authenticator does, with a
// "none" attestation, so tests need no browser or recorded fixture.
package passkeytest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/stretchr/testify/require"
)

const credentialIDLen = 32

// Authenticator holds one credential: a P-256 key and a credential ID.
type Authenticator struct {
	CredentialID []byte
	// UserHandle is the handle the server gave at registration. Create sets it.
	UserHandle []byte
	// SignCount is the count in the last assertion. Assert adds one first.
	SignCount uint32
	key       *ecdsa.PrivateKey
}

// New makes an authenticator with a fresh credential.
func New(t testing.TB) *Authenticator {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	id := make([]byte, credentialIDLen)
	_, err = rand.Read(id)
	require.NoError(t, err)
	return &Authenticator{CredentialID: id, key: key}
}

// Create answers the creation options as a browser's navigator.credentials.create
// would, and returns the JSON the browser sends to the server. origin is the
// page's origin. The challenge comes from the options.
func (a *Authenticator) Create(t testing.TB, options []byte, origin string) []byte {
	t.Helper()
	var creation protocol.CredentialCreation
	require.NoError(t, json.Unmarshal(options, &creation))
	pk := creation.Response
	userID, ok := pk.User.ID.(string) // base64url, as the JSON carries it
	require.True(t, ok)
	handle, err := base64.RawURLEncoding.DecodeString(userID)
	require.NoError(t, err)
	a.UserHandle = handle

	clientData, err := json.Marshal(map[string]any{
		"type":      "webauthn.create",
		"challenge": base64.RawURLEncoding.EncodeToString(pk.Challenge),
		"origin":    origin,
	})
	require.NoError(t, err)

	point, err := a.key.PublicKey.Bytes() // 0x04, X, Y
	require.NoError(t, err)
	x, y := point[1:33], point[33:65]
	cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	require.NoError(t, err)

	rpHash := sha256.Sum256([]byte(pk.RelyingParty.ID))
	var authData []byte
	authData = append(authData, rpHash[:]...)
	authData = append(authData, 0x01|0x04|0x40)      // user present, verified, attested data
	authData = append(authData, 0, 0, 0, 0)          // sign count
	authData = append(authData, make([]byte, 16)...) // AAGUID: none
	authData = binary.BigEndian.AppendUint16(authData, credentialIDLen)
	authData = append(authData, a.CredentialID...)
	authData = append(authData, cose...)

	attestation, err := cbor.Marshal(map[string]any{
		"fmt":      "none",
		"attStmt":  map[string]any{},
		"authData": authData,
	})
	require.NoError(t, err)

	enc := base64.RawURLEncoding.EncodeToString
	body, err := json.Marshal(map[string]any{
		"id":    enc(a.CredentialID),
		"rawId": enc(a.CredentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    enc(clientData),
			"attestationObject": enc(attestation),
			"transports":        []string{"internal"},
		},
	})
	require.NoError(t, err)
	return body
}

// Assert answers the request options as navigator.credentials.get would, with
// the sign count one higher than the last one. It returns the JSON the browser
// sends to the server.
func (a *Authenticator) Assert(t testing.TB, options []byte, origin string) []byte {
	t.Helper()
	a.SignCount++
	return a.AssertWithCount(t, options, origin, a.SignCount)
}

// AssertWithCount is Assert with an exact sign count.
func (a *Authenticator) AssertWithCount(t testing.TB, options []byte, origin string, count uint32) []byte {
	t.Helper()
	a.SignCount = count
	var request protocol.CredentialAssertion
	require.NoError(t, json.Unmarshal(options, &request))
	pk := request.Response

	clientData, err := json.Marshal(map[string]any{
		"type":      "webauthn.get",
		"challenge": base64.RawURLEncoding.EncodeToString(pk.Challenge),
		"origin":    origin,
	})
	require.NoError(t, err)

	rpHash := sha256.Sum256([]byte(pk.RelyingPartyID))
	var authData []byte
	authData = append(authData, rpHash[:]...)
	authData = append(authData, 0x01|0x04) // user present, verified
	authData = binary.BigEndian.AppendUint32(authData, count)

	clientHash := sha256.Sum256(clientData)
	signed := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, signed[:])
	require.NoError(t, err)

	enc := base64.RawURLEncoding.EncodeToString
	body, err := json.Marshal(map[string]any{
		"id":    enc(a.CredentialID),
		"rawId": enc(a.CredentialID),
		"type":  "public-key",
		"response": map[string]any{
			"clientDataJSON":    enc(clientData),
			"authenticatorData": enc(authData),
			"signature":         enc(sig),
			"userHandle":        enc(a.UserHandle),
		},
	})
	require.NoError(t, err)
	return body
}
