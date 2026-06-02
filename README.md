# Automatic GitLab Token Rotation for Kubernetes

A Kubernetes controller that automatically rotates GitLab personal access tokens stored in Kubernetes Secrets. It calls the GitLab API to rotate the token before it expires and writes the new token back to the secret, keeping downstream workloads continuously authenticated.

## How it works

The controller watches all secrets in a configured namespace (or cluster-wide). Secrets opted in via annotation are checked on a cron schedule. When a secret is due for rotation the controller:

1. Reads the current token from the secret annotation.
2. Calls `POST /personal_access_tokens/self/rotate` on the GitLab API.
3. Patches the Kubernetes secret with the new token value.
4. For `Opaque` secrets, deletes pods that reference the secret so they restart and pick up the new value.
5. Emits a Kubernetes Event on success or failure.

## Secret types

Two secret types are supported.

### `kubernetes.io/dockerconfigjson`

Used for Docker registry pull secrets. After rotation the controller updates `.dockerconfigjson` with a new Docker auth entry containing the rotated token.

```yaml
apiVersion: v1
kind: Secret
type: kubernetes.io/dockerconfigjson
metadata:
  name: regcred
  namespace: default
  annotations:
    gitlab-token-rotate/enabled: "true"
    gitlab-token-rotate/gitlab-endpoint: https://gitlab.example.com/api/v4
    # token must have self_rotate scope, will be rotated within 1m
    gitlab-token-rotate/init-token: glpat-xxxxxxxxxxxxxxxxxxxx
    gitlab-token-rotate/login: user.login
    gitlab-token-rotate/registry: registry.gitlab.example.com
data:
  .dockerconfigjson: e30=
```

### `Opaque`

Used for secrets that expose a token as an environment variable. After rotation the controller updates `GITLAB_TOKEN` (or the key set in `gitlab-token-rotate/env-name`) with the new token value, then deletes pods that reference the secret so they restart and pick it up.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: gitlab-token
  namespace: default
  annotations:
    gitlab-token-rotate/enabled: "true"
    gitlab-token-rotate/gitlab-endpoint: https://gitlab.example.com/api/v4
    # token must have self_rotate scope, will be rotated within 1m
    gitlab-token-rotate/init-token: glpat-xxxxxxxxxxxxxxxxxxxx
data:
  GITLAB_TOKEN: <base64-encoded-token>
```

## Annotations reference

### Common (both secret types)

| Annotation | Required | Default | Description |
|---|---|---|---|
| `gitlab-token-rotate/enabled` | yes | — | Set to `"true"` to enable rotation |
| `gitlab-token-rotate/gitlab-endpoint` | yes | — | GitLab API base URL, e.g. `https://gitlab.com/api/v4` |
| `gitlab-token-rotate/token` | yes* | — | Current live token. Updated automatically after each rotation |
| `gitlab-token-rotate/init-token` | no | — | Bootstrap token used only when `token` is empty (first rotation) |
| `gitlab-token-rotate/schedule` | no | `* * * * *` | Cron expression controlling when rotation is checked |
| `gitlab-token-rotate/validity` | no | `70d` (flag default) | `time.Duration` string for how long the new token should be valid |
| `gitlab-token-rotate/updated-at` | — | — | RFC3339 timestamp written by the controller after each rotation |

\* Either `token` or `init-token` must be set for the first rotation.

### `kubernetes.io/dockerconfigjson` only

| Annotation | Required | Default | Description |
|---|---|---|---|
| `gitlab-token-rotate/login` | yes | — | Username to use in the Docker auth entry |
| `gitlab-token-rotate/registry` | yes | — | Registry URL(s), comma-separated |
| `gitlab-token-rotate/extra-auth` | no | — | Extra static auth entries, comma-separated `registry:auth` pairs |

### `Opaque` only

| Annotation | Required | Default | Description |
|---|---|---|---|
| `gitlab-token-rotate/env-name` | no | `GITLAB_TOKEN` | Key written into secret `data` |

## Bootstrap flow

On first use, set `init-token` to a valid token with the `self_rotate` scope. The controller will rotate it on the first scheduled run and store the new token in `token`. Subsequent rotations read from `token` only.

```yaml
annotations:
  gitlab-token-rotate/enabled: "true"
  gitlab-token-rotate/gitlab-endpoint: https://gitlab.example.com/api/v4
  gitlab-token-rotate/init-token: glpat-xxxxxxxxxxxxxxxxxxxx  # set once, controller takes over
```

## Deployment

### Helm

```bash
helm repo add gitlab-token-rotate https://maksim-paskal.github.io/gitlab-token-rotate
helm repo update
helm upgrade gitlab-token-rotate gitlab-token-rotate/gitlab-token-rotate \
  --install \
  --namespace gitlab-token-rotate \
  --create-namespace
```

The Helm chart creates a `ServiceAccount`, `ClusterRole`, and `ClusterRoleBinding` granting the controller the minimum permissions it needs:

| Resource | Verbs |
|---|---|
| `secrets` | `get`, `list`, `patch` |
| `pods` | `get`, `list`, `delete` |
| `events` | `create` |

### CLI flags

| Flag | Default | Description |
|---|---|---|
| `-namespace` | `$NAMESPACE` | Namespace to watch. Empty = all namespaces |
| `-interval` | `1m` | How often to scan for secrets to rotate |
| `-timeout` | `5m` | Per-secret operation timeout |
| `-token-rotation` | `48h` | Minimum time between rotations |
| `-token-validity` | `70d` | Validity period requested from GitLab |
| `-grace-period` | `5s` | Shutdown grace period |
| `-kubeconfig` | `$KUBECONFIG` | Path to kubeconfig (omit when running in-cluster) |
| `-debug` | `false` | Enable debug logging |

## Observability

The controller exposes an HTTP server on port `8080`.

| Path | Description |
|---|---|
| `/healthz` | Returns `200 OK` while running, `503` during shutdown |
| `/metrics` | Prometheus metrics |

### Prometheus metrics

| Metric | Type | Description |
|---|---|---|
| `gitlab_token_rotate_secrets{secret_namespace, secret}` | Gauge | `1` for each tracked secret |
| `gitlab_token_rotate_secrets_last_updated{secret_namespace, secret}` | Gauge | Unix timestamp of last successful rotation |
| `gitlab_token_rotate_errors_total` | Counter | Total number of rotation errors |

The Helm chart annotates the pod for automatic Prometheus scraping:

```yaml
prometheus.io/scrape: "true"
prometheus.io/path: /metrics
prometheus.io/port: "8080"
```

## Development

```bash
# run tests and lint
make test

# build multi-arch image and push
make build
```
