package tokenenv_test

import (
	"testing"

	"github.com/maksim-paskal/gitlab-token-rotate/pkg/processor/tokenenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestTokenEnv(t *testing.T) {
	t.Parallel()

	processor := tokenenv.NewProcessTokenEnv(corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"gitlab-token-rotate/env-name": "TEST_TOKEN",
			},
		},
	})

	require.NoError(t, processor.Validate())

	data, err := processor.GetData("new-token-value")
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"TEST_TOKEN": "bmV3LXRva2VuLXZhbHVl",
	}, data)
}

func TestTokenEnvDefaultName(t *testing.T) {
	t.Parallel()

	processor := tokenenv.NewProcessTokenEnv(corev1.Secret{})

	data, err := processor.GetData("value")
	require.NoError(t, err)
	assert.Contains(t, data, "GITLAB_TOKEN", "should use GITLAB_TOKEN when env-name annotation is absent")
}

func TestPostUpdate(t *testing.T) { //nolint:funlen
	t.Parallel()

	const (
		secretName = "test-secret"
		namespace  = "default"
	)

	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-volume", Namespace: namespace},
			Spec: corev1.PodSpec{
				Volumes: []corev1.Volume{{
					Name: "secret",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{SecretName: secretName},
					},
				}},
				Containers: []corev1.Container{{Name: "c"}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-env-ref", Namespace: namespace},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "c",
					Env: []corev1.EnvVar{{
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
								Key:                  "token",
							},
						},
					}},
				}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-env-from", Namespace: namespace},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name: "c",
					EnvFrom: []corev1.EnvFromSource{{
						SecretRef: &corev1.SecretEnvSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
						},
					}},
				}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-init-container", Namespace: namespace},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "c"}},
				InitContainers: []corev1.Container{{
					Name: "init",
					Env: []corev1.EnvVar{{
						ValueFrom: &corev1.EnvVarSource{
							SecretKeyRef: &corev1.SecretKeySelector{
								LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
								Key:                  "token",
							},
						},
					}},
				}},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-unrelated", Namespace: namespace},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Name: "c"}},
			},
		},
	}

	fakeClient := fake.NewClientset(
		&pods[0], &pods[1], &pods[2], &pods[3], &pods[4],
	)

	proc := tokenenv.NewProcessTokenEnv(corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: namespace},
	})
	proc.KubeClient = fakeClient

	require.NoError(t, proc.PostUpdate(t.Context()))

	deleted := []string{"pod-volume", "pod-env-ref", "pod-env-from", "pod-init-container"}
	for _, name := range deleted {
		_, err := fakeClient.CoreV1().Pods(namespace).Get(t.Context(), name, metav1.GetOptions{})
		assert.True(t, k8serrors.IsNotFound(err), "expected pod %s to be deleted", name)
	}

	_, err := fakeClient.CoreV1().Pods(namespace).Get(t.Context(), "pod-unrelated", metav1.GetOptions{})
	assert.NoError(t, err, "expected pod-unrelated to still exist")
}
