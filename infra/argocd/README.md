## 1. Что у тебя уже есть

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: moiqr-app-of-apps
  namespace: argocd
spec:
  project: default
  source:
    repoURL: git@github.com:FedorovTeam/mono-repo.git
    targetRevision: main
    path: argocd
    directory:
      recurse: true
  destination:
    server: https://kubernetes.default.svc
    namespace: argocd
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

Т.е. ArgoCD будет пытаться ходить по **SSH** в `git@github.com:FedorovTeam/mono-repo.git`. Значит нам нужен **SSH-ключ** в ArgoCD и Deploy Key в GitHub.

---

## 2. Генерируем SSH-ключ и добавляем в GitHub

На своей машине (не в репозитории!):

```bash
ssh-keygen -t ed25519 -C "argocd-deploy-key" -f argocd-mono-repo
```

Получишь два файла:

- `argocd-mono-repo` — **приватный ключ**
- `argocd-mono-repo.pub` — **публичный ключ**

Дальше:

1. Идёшь в GitHub → репозиторий `FedorovTeam/mono-repo`.
2. **Settings → Deploy keys → Add deploy key**.
3. Name: `argocd-mono-repo`

   Key: содержимое `argocd-mono-repo.pub`

   ✅ ставишь галочку **Allow read access** (write не нужен).

---

## 3. Создаём секрет репозитория для ArgoCD

Теперь нужно сказать ArgoCD: “вот ключ, вот URL репозитория”.

Создаём файл, например `manifests/argocd/repo-mono-repo.yaml`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: repo-mono-repo
  namespace: argocd
  labels:
    # Очень важно: говорит ArgoCD, что это секрет с настройками репозитория
    argocd.argoproj.io/secret-type: repository
stringData:
  # Должен в точности совпадать с .spec.source.repoURL в Application
  url: git@github.com:FedorovTeam/mono-repo.git
  sshPrivateKey: |-
    -----BEGIN OPENSSH PRIVATE KEY-----
    ТУТ_ТВОЙ_ПРИВАТНЫЙ_КЛЮЧ_из_argocd-mono-repo
    -----END OPENSSH PRIVATE KEY-----
  # Опционально, просто человекочитаемое имя
  name: mono-repo
```

> ⚠️ В репозиторий реальный приватный ключ лучше не коммитить.
>
>
> Варианты:
>
> - применить этот файл **локально через `kubectl apply` и не класть в Git**;
> - или использовать `sops`/`SealedSecrets`/`External Secrets`, если хочешь GitOps до конца.

Применяем:

```bash
kubectl apply -f manifests/argocd/repo-mono-repo.yaml
```

После этого ArgoCD должен увидеть новый репозиторий (в UI на вкладке **Settings → Repositories** он будет виден).

---

## 4. Применяем App of Apps

Если ты ещё не применял сам `Application`:

```bash
kubectl apply -f manifests/argocd/moiqr-app-of-apps.yaml
```

Дальше:

- заходишь в ArgoCD UI на `https://argocd.tech.fedorov-team.tech`,
- логинишься (`admin` + пароль из секрета `argocd-initial-admin-secret`),
- видишь приложение `moiqr-app-of-apps`,
- нажимаешь **Sync** (если авто-синк не сработал первой).

---

## 5. Про known_hosts и SSH

Ты в values для ArgoCD уже добавлял такие env:

```yaml
repoServer:
  env:
    - name: "SSH_AUTH_SOCK"
      value = ""
    - name: "ARGOCD_GIT_SSH_COMMAND"
      value: "ssh -i /ssh/id_rsa -o StrictHostKeyChecking=yes"
```

Для обычного случая это **не обязательно** — ArgoCD уже поставляется со своим `known_hosts`, в котором есть GitHub.

Минимально достаточно **секрета репозитория** с `sshPrivateKey`, как выше.

Если всё заведётся — я бы эти env вообще убрал из values, чтобы не усложнять себе жизнь.