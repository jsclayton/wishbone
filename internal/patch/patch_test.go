package patch_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"jsclayton.dev/wishbone/internal/patch"
)

type body struct {
	Name patch.Optional[string] `json:"name"`
	Age  patch.Optional[int]    `json:"age"`
}

func decode(t *testing.T, raw string) body {
	t.Helper()
	var b body
	require.NoError(t, json.Unmarshal([]byte(raw), &b))
	return b
}

func TestAnAbsentFieldIsNotSet(t *testing.T) {
	b := decode(t, `{"age": 3}`)

	require.False(t, b.Name.Set)
	require.Nil(t, b.Name.Value)
	require.False(t, b.Name.Clears())
}

func TestANullFieldIsSetAndClears(t *testing.T) {
	b := decode(t, `{"name": null}`)

	require.True(t, b.Name.Set)
	require.Nil(t, b.Name.Value)
	require.True(t, b.Name.Clears())
}

func TestAFieldWithAValueIsSetToIt(t *testing.T) {
	b := decode(t, `{"name": "Ada", "age": 0}`)

	require.True(t, b.Name.Set)
	require.Equal(t, "Ada", *b.Name.Value)
	require.False(t, b.Name.Clears())
	require.True(t, b.Age.Set, "a zero value is still a value")
	require.Equal(t, 0, *b.Age.Value)
}

func TestAFieldWithTheWrongTypeIsAnError(t *testing.T) {
	var b body
	require.Error(t, json.Unmarshal([]byte(`{"age": "three"}`), &b))
}
