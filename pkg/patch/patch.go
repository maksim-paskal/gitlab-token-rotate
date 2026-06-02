package patch

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"time"

	"github.com/adhocore/gronx"
	"github.com/maksim-paskal/gitlab-token-rotate/pkg/client"
	"github.com/maksim-paskal/gitlab-token-rotate/pkg/consts"
	"github.com/maksim-paskal/gitlab-token-rotate/pkg/processor"
	"github.com/pkg/errors"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AnnotationNamespace = consts.AnnotationNamespace

	AnnotationKeyEnabled   = AnnotationNamespace + "/enabled"
	AnnotationKeySchedule  = AnnotationNamespace + "/schedule"
	AnnotationKeyUpdated   = AnnotationNamespace + "/updated-at"
	AnnotationKeyInitToken = AnnotationNamespace + "/init-token"
	AnnotationKeyToken     = AnnotationNamespace + "/token"
	AnnotationKeyEndpoint  = AnnotationNamespace + "/gitlab-endpoint"
	AnnotationKeyValidity  = AnnotationNamespace + "/validity"
)

func NewSecret(secret corev1.Secret) *Secret {
	return &Secret{
		secret:        secret,
		Name:          secret.Name,
		Namespace:     secret.Namespace,
		Type:          string(secret.Type),
		Now:           time.Now(),
		TokenValidity: 60 * 24 * time.Hour,
		TokenRotation: 2 * 24 * time.Hour,
		OnDebug: func(msg string, args ...any) {
			slog.With("namespace", secret.Namespace, "name", secret.Name).Debug(msg, args...)
		},
	}
}

type NewTokenParams struct {
	CurrentToken   string
	GitlabEndpoint string
	Validity       time.Duration
}

type Secret struct {
	secret        corev1.Secret
	Name          string
	Namespace     string
	Type          string
	TokenValidity time.Duration
	TokenRotation time.Duration
	Now           time.Time
	OnDebug       func(msg string, args ...any)
	OnNewToken    func(params *NewTokenParams) (*gitlab.PersonalAccessToken, error)
}

type SecretPatchMetadata struct {
	Annotations map[string]string `json:"annotations"`
}
type SecretPatch struct {
	Metadata SecretPatchMetadata `json:"metadata"`
	Data     map[string]string   `json:"data,omitempty"`
}

func (s *Secret) OnSchedule(on time.Time) bool {
	gron := gronx.New()

	schedule := "* * * * *" // default to every minute

	if userSchedule := s.secret.Annotations[AnnotationKeySchedule]; gron.IsValid(userSchedule) {
		schedule = userSchedule
	}

	ok, err := gron.IsDue(schedule, on.Truncate(time.Minute))
	if err != nil {
		s.OnDebug("Error parsing schedule", "error", err.Error(), "schedule", schedule)

		return false
	}

	s.OnDebug("Checking schedule", "schedule", schedule, "is_due", ok, "time", on.String())

	return ok
}

type PatchResult struct {
	processor processor.Processor

	SecretPatch SecretPatch
	Token       *gitlab.PersonalAccessToken
}

func (p *PatchResult) SecretPatchJSON() ([]byte, error) {
	patchData, err := json.Marshal(p.SecretPatch)
	if err != nil {
		return nil, errors.Wrap(err, "error marshaling secret patch")
	}

	return patchData, nil
}

func (p *PatchResult) PostUpdate(ctx context.Context) error {
	if err := p.processor.PostUpdate(ctx); err != nil {
		return errors.Wrap(err, "error in processor.PostUpdate")
	}

	return nil
}

type SecretState string

const SecretStateValid SecretState = "valid"

const (
	SecretStateNotEnabled      SecretState = "not-enabled"
	SecretStateInvalidType     SecretState = "invalid-type"
	SecretStateNotScheduled    SecretState = "not-scheduled"
	SecretStateRecentlyUpdated SecretState = "recently-updated"
)

func (s *Secret) LastUpdated() time.Time {
	lastUpdated := time.Time{}

	if datetime, err := time.Parse(time.RFC3339, s.secret.Annotations[AnnotationKeyUpdated]); err == nil {
		lastUpdated = datetime
	}

	return lastUpdated
}

func (s *Secret) GetState() SecretState {
	if value, ok := s.secret.Annotations[AnnotationKeyEnabled]; !ok || value != "true" {
		return SecretStateNotEnabled
	}

	if !slices.Contains([]corev1.SecretType{corev1.SecretTypeDockerConfigJson, corev1.SecretTypeOpaque}, s.secret.Type) {
		return SecretStateInvalidType
	}

	if !s.OnSchedule(s.Now) {
		return SecretStateNotScheduled
	}

	if time.Since(s.LastUpdated()) < s.TokenRotation {
		return SecretStateRecentlyUpdated
	}

	return SecretStateValid
}

func (s *Secret) GetPatch(ctx context.Context) (*PatchResult, error) { //nolint:funlen
	if state := s.GetState(); state != SecretStateValid {
		return nil, errors.Errorf("secret state is %s", state)
	}

	slog.Info("Processing secret")

	processor, err := processor.NewProcessor(s.secret)
	if err != nil {
		return nil, errors.Wrap(err, "error in processor.NewProcessor")
	}

	if err := processor.Validate(); err != nil {
		return nil, errors.Wrap(err, "error in validation")
	}

	currentToken := s.secret.Annotations[AnnotationKeyToken]

	if initToken := s.secret.Annotations[AnnotationKeyInitToken]; currentToken == "" {
		currentToken = initToken
	}

	if currentToken == "" {
		return nil, errors.Errorf("current token annotation %s is empty", AnnotationKeyToken)
	}

	gitlabEndpoint := s.secret.Annotations[AnnotationKeyEndpoint]

	if gitlabEndpoint == "" {
		return nil, errors.Errorf("gitlab endpoint annotation %s is missing", AnnotationKeyEndpoint)
	}

	validity := s.TokenValidity
	if duration, err := time.ParseDuration(s.secret.Annotations[AnnotationKeyValidity]); err == nil {
		validity = duration
	}

	newToken, err := s.OnNewToken(&NewTokenParams{
		CurrentToken:   currentToken,
		GitlabEndpoint: gitlabEndpoint,
		Validity:       validity,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error in a.newToken")
	}

	slog.Info("Rotated token", "new_token_id", newToken.ID, "expires_at", newToken.ExpiresAt)

	data, err := processor.GetData(newToken.Token)
	if err != nil {
		return nil, errors.Wrap(err, "error in processor.GetData")
	}

	newPatch := SecretPatch{
		Metadata: SecretPatchMetadata{
			Annotations: map[string]string{
				AnnotationKeyToken:   newToken.Token,
				AnnotationKeyUpdated: s.Now.Format(time.RFC3339),
			},
		},
		Data: data,
	}

	return &PatchResult{
		processor:   processor,
		SecretPatch: newPatch,
		Token:       newToken,
	}, nil
}

type EventParam struct {
	Type    string
	Reason  string
	Message string
}

func (s *Secret) AddEvent(ctx context.Context, params EventParam) {
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "gitlab-token-rotate-",
			Namespace:    s.secret.Namespace,
		},
		ReportingController: "gitlab-token-rotate",
		LastTimestamp:       metav1.NewTime(time.Now()),
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Secret",
			Namespace: s.secret.Namespace,
			Name:      s.secret.Name,
		},
		Reason:  params.Reason,
		Message: params.Message,
		Type:    params.Type,
	}

	_, err := client.GetKubernetesClient().CoreV1().Events(event.InvolvedObject.Namespace).Create(ctx,
		event,
		metav1.CreateOptions{},
	)
	if err != nil {
		slog.Error("Error creating event", "error", err.Error())
	}
}
