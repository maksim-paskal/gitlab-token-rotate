Helm chart repository for [gitlab-token-rotate](https://github.com/maksim-paskal/gitlab-token-rotate).

## Usage

```bash
helm repo add gitlab-token-rotate https://maksim-paskal.github.io/gitlab-token-rotate
helm repo update
helm upgrade gitlab-token-rotate gitlab-token-rotate/gitlab-token-rotate \
  --install \
  --namespace gitlab-token-rotate \
  --create-namespace
```
