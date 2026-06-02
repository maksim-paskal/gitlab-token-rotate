package dockerauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/maksim-paskal/gitlab-token-rotate/pkg/consts"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
)

const (
	annotationKeyLogin     = consts.AnnotationNamespace + "/login"
	annotationKeyRegistry  = consts.AnnotationNamespace + "/registry"
	annotationKeyExtraAuth = consts.AnnotationNamespace + "/extra-auth"
)

type ProcessDockerAuth struct {
	dockerLogin    string
	dockerRegistry string
	extraAuth      string
}

func NewProcessDockerAuth(secret corev1.Secret) *ProcessDockerAuth {
	return &ProcessDockerAuth{
		dockerLogin:    secret.Annotations[annotationKeyLogin],
		dockerRegistry: secret.Annotations[annotationKeyRegistry],
		extraAuth:      secret.Annotations[annotationKeyExtraAuth],
	}
}

func (p *ProcessDockerAuth) Validate() error {
	if p.dockerLogin == "" {
		return errors.New("DockerLogin is required")
	}

	if p.dockerRegistry == "" {
		return errors.New("DockerRegistry is required")
	}

	return nil
}

type dockerConfigAuth struct {
	UserName string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Email    string `json:"email,omitempty"`
	Auth     string `json:"auth,omitempty"`
}

type dockerConfig struct {
	Auths map[string]dockerConfigAuth `json:"auths"`
}

func (d dockerConfig) JSON() ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling docker config")
	}

	return data, nil
}

func (d dockerConfig) Encoded() (string, error) {
	data, err := d.JSON()
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

func (p *ProcessDockerAuth) GetData(newToken string) (map[string]string, error) {
	newDockerConfig := dockerConfig{
		Auths: make(map[string]dockerConfigAuth),
	}

	for _, registryEntry := range strings.Split(p.dockerRegistry, ",") {
		registryEntry = strings.TrimSpace(registryEntry)
		if len(registryEntry) == 0 {
			continue
		}

		newDockerConfig.Auths[registryEntry] = dockerConfigAuth{
			Auth: base64.StdEncoding.EncodeToString([]byte(p.dockerLogin + ":" + newToken)),
		}
	}

	for _, extraAuth := range strings.Split(p.extraAuth, ",") {
		extraAuth = strings.TrimSpace(extraAuth)
		if len(extraAuth) == 0 {
			continue
		}

		parts := strings.SplitN(extraAuth, ":", 2)
		if len(parts) != 2 {
			continue
		}

		newDockerConfig.Auths[parts[0]] = dockerConfigAuth{
			Auth: base64.StdEncoding.EncodeToString([]byte(parts[1])),
		}
	}

	encoded, err := newDockerConfig.Encoded()
	if err != nil {
		return nil, err
	}

	return map[string]string{
		".dockerconfigjson": encoded,
	}, nil
}

func (p *ProcessDockerAuth) PostUpdate(ctx context.Context) error {
	return nil
}
