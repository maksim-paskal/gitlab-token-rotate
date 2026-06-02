package tokenenv

import (
	"context"
	"encoding/base64"
	"log/slog"

	"github.com/maksim-paskal/gitlab-token-rotate/pkg/client"
	"github.com/maksim-paskal/gitlab-token-rotate/pkg/consts"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const annotationKeyEnvName = consts.AnnotationNamespace + "/env-name"

type ProcessTokenEnv struct {
	EnvName         string
	SecretNamespace string
	SecretName      string
	KubeClient      kubernetes.Interface
}

func (p *ProcessTokenEnv) getKubeClient() kubernetes.Interface {
	if p.KubeClient != nil {
		return p.KubeClient
	}

	return client.GetKubernetesClient()
}

func NewProcessTokenEnv(secret corev1.Secret) *ProcessTokenEnv {
	return &ProcessTokenEnv{
		EnvName:         secret.Annotations[annotationKeyEnvName],
		SecretNamespace: secret.Namespace,
		SecretName:      secret.Name,
	}
}

func (p *ProcessTokenEnv) Validate() error {
	return nil
}

func (p *ProcessTokenEnv) GetData(newToken string) (map[string]string, error) {
	envName := p.EnvName
	if envName == "" {
		envName = "GITLAB_TOKEN"
	}

	return map[string]string{
		envName: base64.StdEncoding.EncodeToString([]byte(newToken)),
	}, nil
}

func (p *ProcessTokenEnv) podHasSecret(pod corev1.Pod) bool { //nolint:cyclop
	for _, vol := range pod.Spec.Volumes {
		if vol.Secret != nil && vol.Secret.SecretName == p.SecretName {
			return true
		}
	}

	for _, containers := range [][]corev1.Container{pod.Spec.Containers, pod.Spec.InitContainers} {
		for _, container := range containers {
			for _, env := range container.Env {
				if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil && env.ValueFrom.SecretKeyRef.Name == p.SecretName {
					return true
				}
			}

			for _, envFrom := range container.EnvFrom {
				if envFrom.SecretRef != nil && envFrom.SecretRef.Name == p.SecretName {
					return true
				}
			}
		}
	}

	return false
}

func (p *ProcessTokenEnv) restartPods(ctx context.Context) error {
	podList, err := p.getKubeClient().CoreV1().Pods(p.SecretNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "error listing pods")
	}

	for _, pod := range podList.Items {
		if !p.podHasSecret(pod) {
			continue
		}

		slog.Info("reloading pod to pick up secret change", "namespace", pod.Namespace, "pod", pod.Name)

		err := p.getKubeClient().CoreV1().Pods(p.SecretNamespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
		if err != nil {
			return errors.Wrapf(err, "error deleting pod to reload secret: %s/%s", pod.Namespace, pod.Name)
		}
	}

	return nil
}

func (p *ProcessTokenEnv) PostUpdate(ctx context.Context) error {
	if err := p.restartPods(ctx); err != nil {
		return errors.Wrap(err, "error restarting pods")
	}

	return nil
}
