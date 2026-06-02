package processor

import (
	"context"
	"errors"

	"github.com/maksim-paskal/gitlab-token-rotate/pkg/processor/dockerauth"
	"github.com/maksim-paskal/gitlab-token-rotate/pkg/processor/tokenenv"
	corev1 "k8s.io/api/core/v1"
)

type Processor interface {
	Validate() error
	GetData(newToken string) (map[string]string, error)
	PostUpdate(ctx context.Context) error
}

func NewProcessor(secret corev1.Secret) (Processor, error) {
	switch secret.Type { //nolint:exhaustive
	case corev1.SecretTypeDockerConfigJson:
		return dockerauth.NewProcessDockerAuth(secret), nil
	case corev1.SecretTypeOpaque:
		return tokenenv.NewProcessTokenEnv(secret), nil
	default:
		return nil, errors.New("unknown secret type " + string(secret.Type))
	}
}
