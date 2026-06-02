package internal

import (
	"context"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/maksim-paskal/gitlab-token-rotate/pkg/client"
	"github.com/maksim-paskal/gitlab-token-rotate/pkg/metrics"
	"github.com/maksim-paskal/gitlab-token-rotate/pkg/patch"
	"github.com/pkg/errors"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func NewApplication() *Application {
	return &Application{
		KubeConfig: os.Getenv("KUBECONFIG"),
		Namespace:  os.Getenv("NAMESPACE"),
		Interval:   time.Minute,
		Timeout:    5 * time.Minute,

		TokenRotation: 2 * 24 * time.Hour,
		TokenValidity: 70 * 24 * time.Hour,
	}
}

type Application struct {
	// How often to check for tokens to rotate
	Interval time.Duration

	// How long to wait for Kubernetes API calls
	Timeout time.Duration

	// Kubernetes config and namespace to watch
	KubeConfig string

	// Namespace to watch for secrets
	Namespace string

	// How often to rotate tokens
	TokenRotation time.Duration

	// How long tokens should be valid for
	TokenValidity time.Duration
}

func (a *Application) Handler(ctx context.Context) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if ctx.Err() == nil {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})

	mux.Handle("/metrics", metrics.GetHandler())

	return mux
}

func (a *Application) sleepContext(ctx context.Context, duration time.Duration) {
	slog.Debug("Sleeping", "duration", duration.String())

	select {
	case <-ctx.Done():
	case <-time.After(duration):
	}
}

func (a *Application) newToken(token, endpoint string, validity time.Duration) (*gitlab.PersonalAccessToken, error) {
	gitlabClient, err := gitlab.NewClient(token, gitlab.WithBaseURL(endpoint))
	if err != nil {
		return nil, errors.Wrap(err, "error in gitlab.NewClient")
	}

	expire := gitlab.ISOTime(time.Now().Add(validity))

	newToken, _, err := gitlabClient.PersonalAccessTokens.RotatePersonalAccessTokenSelf(&gitlab.RotatePersonalAccessTokenOptions{
		ExpiresAt: &expire,
	})
	if err != nil {
		return nil, errors.Wrap(err, "error in RotatePersonalAccessTokenSelf")
	}

	return newToken, nil
}

func (a *Application) processSecret(ctx context.Context, secret *patch.Secret) error {
	slog := slog.With("secret", secret.Name, "namespace", secret.Namespace)

	secret.OnNewToken = func(params *patch.NewTokenParams) (*gitlab.PersonalAccessToken, error) {
		return a.newToken(params.CurrentToken, params.GitlabEndpoint, params.Validity)
	}

	newPatch, err := secret.GetPatch(ctx)
	if err != nil {
		return errors.Wrap(err, "error in newSecret.GetPatch")
	}

	if newPatch == nil {
		slog.Debug("No patch needed")

		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	patchJSON, err := newPatch.SecretPatchJSON()
	if err != nil {
		return errors.Wrap(err, "error serializing patch")
	}

	for ctx.Err() == nil {
		_, err = client.GetKubernetesClient().CoreV1().Secrets(secret.Namespace).Patch(ctx,
			secret.Name,
			types.MergePatchType,
			patchJSON,
			metav1.PatchOptions{},
		)
		if err == nil {
			if err := newPatch.PostUpdate(ctx); err != nil {
				slog.Error("Error in PostUpdate", "error", err.Error())
			}

			secret.AddEvent(ctx, patch.EventParam{
				Reason:  "ScheduleSecretRotation",
				Message: "Successfully rotated secret, expires at " + newPatch.Token.ExpiresAt.String(),
				Type:    corev1.EventTypeNormal,
			})

			return nil
		}

		slog.Error("Error patching secret, retrying", "error", err.Error())
		a.sleepContext(ctx, 5*time.Second)
	}

	return errors.New("failed to patch secret: " + err.Error())
}

func (a *Application) getSecrets(ctx context.Context) ([]*patch.Secret, error) {
	secretsList, err := client.GetKubernetesClient().CoreV1().Secrets(a.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "error listing secrets")
	}

	metrics.Secrets.Reset()
	metrics.LastUpdated.Reset()

	secrets := make([]*patch.Secret, 0)

	for _, secret := range secretsList.Items {
		slog := slog.With("secret", secret.Name, "namespace", secret.Namespace)
		secret := patch.NewSecret(secret)

		secret.TokenRotation = a.TokenRotation
		secret.TokenValidity = a.TokenValidity

		state := secret.GetState()

		if state == patch.SecretStateNotEnabled {
			continue
		}

		metrics.Secrets.WithLabelValues(
			secret.Namespace,
			secret.Name,
		).Set(1)

		metrics.LastUpdated.WithLabelValues(
			secret.Namespace,
			secret.Name,
		).Set(float64(secret.LastUpdated().Unix()))

		if state != patch.SecretStateValid {
			slog.Debug("Skipping secret", "state", state)

			continue
		}

		secrets = append(secrets, secret)
	}

	return secrets, nil
}

func (a *Application) ScheduleSecretRotation(ctx context.Context) {
	slog.Info("Starting secret rotation scheduler")

	firstStart := true

	for ctx.Err() == nil {
		// run initial iteration on process start
		if firstStart {
			firstStart = false
		} else {
			a.sleepContext(ctx, a.Interval)
		}

		secrets, err := a.getSecrets(ctx)
		if err != nil {
			metrics.Errors.Inc()
			slog.Error("Error getting secrets", "error", err.Error())

			continue
		}

		for _, secret := range secrets {
			func() {
				ctx, cancel := context.WithTimeout(ctx, a.Timeout)
				defer cancel()

				if err := a.processSecret(ctx, secret); err != nil {
					metrics.Errors.Inc()
					slog.Error("Error processing secret", "error", err.Error())

					secret.AddEvent(ctx, patch.EventParam{
						Reason:  "ScheduleSecretRotation",
						Message: err.Error(),
						Type:    corev1.EventTypeWarning,
					})
				}
			}()
		}
	}
}

func (a *Application) Start(ctx context.Context) {
	slog.Info("Start application", "application", a)

	serverCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Create a new HTTP server with the Handler function as the handler
	server := http.Server{
		Addr:         "0.0.0.0:8080",
		Handler:      a.Handler(serverCtx),
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return serverCtx
		},
	}

	go func() {
		<-ctx.Done()
		slog.Warn("Shutting down server")

		_ = server.Shutdown(context.Background()) //nolint:contextcheck
	}()

	// Start the server and log any errors
	slog.Info("Starting proxy server", "address", server.Addr)

	err := server.ListenAndServe()
	if err != nil && ctx.Err() == nil {
		log.Fatal("Error starting proxy server: ", err) //nolint:gocritic
	}
}
