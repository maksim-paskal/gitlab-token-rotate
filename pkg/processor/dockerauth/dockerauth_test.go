package dockerauth_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/maksim-paskal/gitlab-token-rotate/pkg/processor/dockerauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestProcessDockerAuth(t *testing.T) {
	t.Parallel()

	processor := dockerauth.NewProcessDockerAuth(corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"gitlab-token-rotate/login":      "testuser",
				"gitlab-token-rotate/registry":   "test.registry.com",
				"gitlab-token-rotate/extra-auth": "extra.registry.com:extraToken",
			},
		},
	})

	require.NoError(t, processor.Validate())

	secretData, err := processor.GetData("newToken")
	require.NoError(t, err)
	require.NotNil(t, secretData)

	assert.Contains(t, secretData, ".dockerconfigjson")
	assert.NotEmpty(t, secretData[".dockerconfigjson"])

	dockerConfigJSON, err := base64.StdEncoding.DecodeString(secretData[".dockerconfigjson"])
	require.NoError(t, err)
	assert.NotEmpty(t, dockerConfigJSON)

	assert.JSONEq(t, `{"auths":{"extra.registry.com":{"auth":"ZXh0cmFUb2tlbg=="},"test.registry.com":{"auth":"dGVzdHVzZXI6bmV3VG9rZW4="}}}`, string(dockerConfigJSON))
}

func TestValidateErrors(t *testing.T) {
	t.Parallel()

	t.Run("missing-login", func(t *testing.T) {
		t.Parallel()

		p := dockerauth.NewProcessDockerAuth(corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"gitlab-token-rotate/registry": "registry.example.com",
				},
			},
		})
		assert.Error(t, p.Validate())
	})

	t.Run("missing-registry", func(t *testing.T) {
		t.Parallel()

		p := dockerauth.NewProcessDockerAuth(corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"gitlab-token-rotate/login": "user",
				},
			},
		})
		assert.Error(t, p.Validate())
	})
}

func TestMultipleRegistries(t *testing.T) {
	t.Parallel()

	p := dockerauth.NewProcessDockerAuth(corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"gitlab-token-rotate/login":    "user",
				"gitlab-token-rotate/registry": "registry1.example.com, registry2.example.com",
			},
		},
	})
	require.NoError(t, p.Validate())

	data, err := p.GetData("token")
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(data[".dockerconfigjson"])
	require.NoError(t, err)

	var cfg map[string]map[string]any

	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.Contains(t, cfg["auths"], "registry1.example.com")
	assert.Contains(t, cfg["auths"], "registry2.example.com")
}

func TestInvalidExtraAuth(t *testing.T) {
	t.Parallel()

	p := dockerauth.NewProcessDockerAuth(corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"gitlab-token-rotate/login":      "user",
				"gitlab-token-rotate/registry":   "registry.example.com",
				"gitlab-token-rotate/extra-auth": "no-colon-here",
			},
		},
	})
	require.NoError(t, p.Validate())

	data, err := p.GetData("token")
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(data[".dockerconfigjson"])
	require.NoError(t, err)

	var cfg map[string]map[string]any

	require.NoError(t, json.Unmarshal(raw, &cfg))
	assert.NotContains(t, cfg["auths"], "no-colon-here", "invalid extra-auth entry should be silently skipped")
}

func TestDockerAuthPostUpdate(t *testing.T) {
	t.Parallel()

	p := dockerauth.NewProcessDockerAuth(corev1.Secret{})
	require.NoError(t, p.PostUpdate(t.Context()))
}
