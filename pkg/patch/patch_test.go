package patch_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/maksim-paskal/gitlab-token-rotate/pkg/patch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestOnSchedule(t *testing.T) {
	t.Parallel()

	newPatch := patch.NewSecret(corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				patch.AnnotationKeySchedule: "*/5 * * * *",
			},
		},
	})

	tests := make(map[time.Time]bool)
	tests[time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)] = true
	tests[time.Date(2024, 1, 1, 0, 1, 4, 1, time.UTC)] = false
	tests[time.Date(2024, 1, 1, 0, 2, 0, 0, time.UTC)] = false
	tests[time.Date(2024, 1, 1, 0, 3, 0, 0, time.UTC)] = false
	tests[time.Date(2024, 1, 1, 0, 4, 0, 0, time.UTC)] = false
	tests[time.Date(2024, 1, 1, 0, 5, 1, 0, time.UTC)] = true
	tests[time.Date(2024, 1, 1, 0, 6, 0, 0, time.UTC)] = false
	tests[time.Date(2024, 1, 1, 0, 7, 0, 0, time.UTC)] = false
	tests[time.Date(2024, 1, 1, 0, 8, 0, 0, time.UTC)] = false
	tests[time.Date(2024, 1, 1, 0, 9, 0, 0, time.UTC)] = false
	tests[time.Date(2024, 1, 1, 0, 10, 0, 0, time.UTC)] = true

	for ts, expected := range tests {
		t.Run(ts.String(), func(t *testing.T) {
			t.Parallel()

			if got := newPatch.OnSchedule(ts); got != expected {
				t.Errorf("OnSchedule() = %v, want %v", got, expected)
			}
		})
	}
}

func newTokenFunc(params *patch.NewTokenParams) (*gitlab.PersonalAccessToken, error) {
	return &gitlab.PersonalAccessToken{
		Token:     params.CurrentToken + "-new",
		ExpiresAt: &gitlab.ISOTime{},
	}, nil
}

func TestGetState(t *testing.T) { //nolint:funlen
	t.Parallel()

	noon := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		secret corev1.Secret
		now    time.Time
		state  patch.SecretState
	}{
		{
			name:  "not-enabled-missing",
			state: patch.SecretStateNotEnabled,
		},
		{
			name: "not-enabled-false",
			secret: corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{patch.AnnotationKeyEnabled: "false"},
				},
			},
			state: patch.SecretStateNotEnabled,
		},
		{
			name: "invalid-type",
			secret: corev1.Secret{
				Type: corev1.SecretTypeServiceAccountToken,
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{patch.AnnotationKeyEnabled: "true"},
				},
			},
			state: patch.SecretStateInvalidType,
		},
		{
			name: "not-scheduled",
			secret: corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						patch.AnnotationKeyEnabled:  "true",
						patch.AnnotationKeySchedule: "0 0 * * *", // midnight only
					},
				},
			},
			now:   noon,
			state: patch.SecretStateNotScheduled,
		},
		{
			name: "recently-updated",
			secret: corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						patch.AnnotationKeyEnabled: "true",
						patch.AnnotationKeyUpdated: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
					},
				},
			},
			state: patch.SecretStateRecentlyUpdated,
		},
		{
			name: "valid",
			secret: corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{patch.AnnotationKeyEnabled: "true"},
				},
			},
			now:   noon,
			state: patch.SecretStateValid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := patch.NewSecret(tc.secret)
			if !tc.now.IsZero() {
				s.Now = tc.now
			}

			assert.Equal(t, tc.state, s.GetState())
		})
	}
}

func TestLastUpdated(t *testing.T) {
	t.Parallel()

	s := patch.NewSecret(corev1.Secret{})
	assert.Zero(t, s.LastUpdated())

	ts := time.Now().Truncate(time.Second)
	s2 := patch.NewSecret(corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				patch.AnnotationKeyUpdated: ts.Format(time.RFC3339),
			},
		},
	})
	assert.Equal(t, ts.UTC(), s2.LastUpdated().UTC())
}

func TestSecretPatchJSON(t *testing.T) {
	t.Parallel()

	s := patch.NewSecret(corev1.Secret{
		Type: corev1.SecretTypeDockerConfigJson,
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				patch.AnnotationKeyEnabled:              "true",
				patch.AnnotationKeyToken:                "test-token",
				patch.AnnotationKeyEndpoint:             "https://gitlab.com",
				patch.AnnotationNamespace + "/login":    "user",
				patch.AnnotationNamespace + "/registry": "registry.example.com",
			},
		},
	})
	s.OnNewToken = newTokenFunc

	result, err := s.GetPatch(t.Context())
	require.NoError(t, err)

	patchJSON, err := result.SecretPatchJSON()
	require.NoError(t, err)
	assert.NotEmpty(t, patchJSON)

	var m map[string]any

	require.NoError(t, json.Unmarshal(patchJSON, &m))
}

func TestPatchResultPostUpdate(t *testing.T) {
	t.Parallel()

	s := patch.NewSecret(corev1.Secret{
		Type: corev1.SecretTypeDockerConfigJson,
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				patch.AnnotationKeyEnabled:              "true",
				patch.AnnotationKeyToken:                "test-token",
				patch.AnnotationKeyEndpoint:             "https://gitlab.com",
				patch.AnnotationNamespace + "/login":    "user",
				patch.AnnotationNamespace + "/registry": "registry.example.com",
			},
		},
	})
	s.OnNewToken = newTokenFunc

	result, err := s.GetPatch(t.Context())
	require.NoError(t, err)
	require.NoError(t, result.PostUpdate(t.Context()))
}

func TestPatch(t *testing.T) { //nolint:funlen
	t.Parallel()

	now := time.Now()

	type test struct {
		Name   string
		Secret corev1.Secret

		ExpectError bool
		Expect      *patch.SecretPatch
	}

	tests := []test{
		{
			Name: "not enabled",
			Secret: corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			ExpectError: true,
		},
		{
			Name: "enabled",
			Secret: corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						patch.AnnotationKeyEnabled: "true",
					},
				},
			},
			ExpectError: true,
		},
		{
			Name: "enabled-opaque",
			Secret: corev1.Secret{
				Type: corev1.SecretTypeOpaque,
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						patch.AnnotationKeyEnabled:  "true",
						patch.AnnotationKeyToken:    "some-token",
						patch.AnnotationKeyEndpoint: "https://gitlab.com",
					},
				},
			},
			Expect: &patch.SecretPatch{
				Metadata: patch.SecretPatchMetadata{
					Annotations: map[string]string{
						patch.AnnotationKeyToken:   "some-token-new",
						patch.AnnotationKeyUpdated: now.Format(time.RFC3339),
					},
				},
				Data: map[string]string{
					"GITLAB_TOKEN": "c29tZS10b2tlbi1uZXc=",
				},
			},
		},
		{
			Name: "enabled-dockerconfigjson",
			Secret: corev1.Secret{
				Type: corev1.SecretTypeDockerConfigJson,
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						patch.AnnotationKeyEnabled:  "true",
						patch.AnnotationKeyToken:    "some-token",
						patch.AnnotationKeyEndpoint: "https://gitlab.com",

						patch.AnnotationNamespace + "/login":    "gitlab+deploy-token",
						patch.AnnotationNamespace + "/registry": "https://registry.gitlab.com",
					},
				},
			},
			Expect: &patch.SecretPatch{
				Metadata: patch.SecretPatchMetadata{
					Annotations: map[string]string{
						patch.AnnotationKeyToken:   "some-token-new",
						patch.AnnotationKeyUpdated: now.Format(time.RFC3339),
					},
				},
				Data: map[string]string{
					".dockerconfigjson": "eyJhdXRocyI6eyJodHRwczovL3JlZ2lzdHJ5LmdpdGxhYi5jb20iOnsiYXV0aCI6IloybDBiR0ZpSzJSbGNHeHZlUzEwYjJ0bGJqcHpiMjFsTFhSdmEyVnVMVzVsZHc9PSJ9fX0=",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			newPatch := patch.NewSecret(tc.Secret)
			newPatch.Now = now
			newPatch.OnDebug = func(msg string, args ...any) {
				t.Log(append([]any{msg}, args...)...)
			}
			newPatch.OnNewToken = newTokenFunc

			result, err := newPatch.GetPatch(t.Context())
			if !tc.ExpectError && err != nil {
				require.Fail(t, "unexpected error: %v", err)
			}

			if result == nil && tc.Expect == nil {
				return
			}

			require.NotNil(t, result)
			require.NotNil(t, tc.Expect)

			if tc.Expect != nil {
				assert.Equal(t, tc.Expect.Metadata, result.SecretPatch.Metadata)
				assert.Equal(t, tc.Expect.Data, result.SecretPatch.Data)
			}
		})
	}
}
