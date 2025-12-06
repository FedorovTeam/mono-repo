# infra/modules/ci/argo-workflows/manifests/backend-ci.yaml.tpl
apiVersion: v1
kind: Secret
metadata:
  name: github-secret
  namespace: ${namespace}
data:
  username: ${base64encode(github_username)}
  password: ${base64encode(github_token)}
type: kubernetes.io/basic-auth
---
apiVersion: v1
kind: Secret
metadata:
  name: harbor-robot
  namespace: ${namespace}
  labels:
    app.kubernetes.io/part-of: backend-ci
    app.kubernetes.io/component: credentials
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: ${harbor_dockerconfigjson}
---
apiVersion: v1
kind: Secret
metadata:
  name: argocd-token
  namespace: ${namespace}
type: Opaque
data:
  token: ${base64encode(argocd_token)}

---
apiVersion: argoproj.io/v1alpha1
kind: WorkflowTemplate
metadata:
  name: backend-build
  namespace: ${namespace}
spec:
  serviceAccountName: ci-bot
  entrypoint: build
  templates:
    - name: build
      inputs:
        parameters:
          - name: git_sha
      dag:
        tasks:
          - name: clone
            template: git-clone
            arguments:
              parameters:
                - name: git_sha
                  value: "{{inputs.parameters.git_sha}}"
          - name: test
            dependencies: [clone]
            template: run-tests
          - name: build-image
            dependencies: [test]
            template: kaniko-build
            arguments:
              parameters:
                - name: git_sha
                  value: "{{inputs.parameters.git_sha}}"
          - name: deploy
            dependencies: [build-image]
            template: argocd-deploy
            arguments:
              parameters:
                - name: git_sha
                  value: "{{inputs.parameters.git_sha}}"
    - name: git-clone
      inputs:
        parameters:
          - name: git_sha
      container:
        image: alpine/git
        command: [ "sh", "-c" ]
        args:
          - |
            set -eo pipefail
            ulimit -n 1048576
            REPO="https://$${GIT_USERNAME}:$${GIT_TOKEN}@github.com/${github_repo}.git"
            git clone --depth 1 "$REPO" /repo
            cd /repo
            git fetch --depth 1 origin {{inputs.parameters.git_sha}}
            git checkout {{inputs.parameters.git_sha}}
            cp -R . /workspace
        env:
          - name: GIT_USERNAME
            valueFrom:
              secretKeyRef:
                name: github-secret
                key: username
          - name: GIT_TOKEN
            valueFrom:
              secretKeyRef:
                name: github-secret
                key: password
        volumeMounts:
          - name: workdir
            mountPath: /workspace

    - name: run-tests
      container:
        image: golang:1.23
        command: [ "sh", "-c" ]
        workingDir: /workspace/backend/docling-transformer-service
        args:
          - |
            set -eo pipefail
            ulimit -n 1048576
            go test ./...
        volumeMounts:
          - name: workdir
            mountPath: /workspace

    - name: kaniko-build
      inputs:
        parameters:
          - name: git_sha
      container:
        image: gcr.io/kaniko-project/executor:v1.22.0
        command: [ "sh", "-c" ]
        args:
          - |
            ulimit -n 1048576
            /kaniko/executor \
              --dockerfile=/workspace/backend/docling-transformer-service/Dockerfile \
              --context=/workspace/backend/docling-transformer-service \
              --destination=${harbor_image}:{{inputs.parameters.git_sha}}
        env:
          - name: DOCKER_CONFIG
            value: /kaniko/.docker
        volumeMounts:
          - name: workdir
            mountPath: /workspace

    - name: argocd-deploy
      inputs:
        parameters:
          - name: git_sha
      container:
        image: argoproj/argocd:v2.9.3
        command: ["sh", "-c"]
        args:
          - |
            argocd login ${argocd_server} --insecure --auth-token "$ARGOCD_TOKEN"
            argocd app set ${argocd_app} --helm-set image.tag={{inputs.parameters.git_sha}}
            argocd app sync ${argocd_app}
        env:
          - name: ARGOCD_TOKEN
            valueFrom:
              secretKeyRef:
                name: argocd-token
                key: token
  volumes:
    - name: workdir
      emptyDir: {}
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: github-poller
  namespace: ${namespace}
spec:
  schedule: "${poller_schedule}"
  jobTemplate:
    spec:
      backoffLimit: 1
      template:
        spec:
          serviceAccountName: ci-bot
          restartPolicy: OnFailure
          containers:
            - name: poller
              image: alpine:3.20
              command: ["/bin/sh", "/scripts/poller.sh"]
              env:
                - name: GITHUB_TOKEN
                  valueFrom:
                    secretKeyRef:
                      name: github-secret
                      key: password
              volumeMounts:
                - name: scripts
                  mountPath: /scripts
          volumes:
            - name: scripts
              configMap:
                name: github-poller-script

---
apiVersion: v1
kind: ConfigMap
metadata:
  name: github-poller-state
  namespace: ${namespace}
data:
  last-sha: ""
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: github-poller-script
  namespace: ${namespace}
data:
  poller.sh: |
    #!/bin/sh
    set -eu

    apk add --no-cache curl jq bash kubectl >/dev/null

    NAMESPACE="${namespace}"
    REPO="${github_repo}"
    BRANCH="${github_branch}"
    GH_API="https://api.github.com/repos/${github_repo}/commits/${github_branch}"
    STATE_CM="github-poller-state"

    if [ -z "$${GITHUB_TOKEN:-}" ]; then
      echo "GITHUB_TOKEN is not set"
      exit 1
    fi

    # Получаем последний коммит из GitHub (с авторизацией по токену)
    SHA=$(curl -s -H "Authorization: token $${GITHUB_TOKEN}" "$GH_API" | jq -r '.sha')

    if [ -z "$SHA" ] || [ "$SHA" = "null" ]; then
      echo "Failed to get SHA from GitHub"
      exit 1
    fi

    # Читаем сохранённый SHA из ConfigMap
    STORED_SHA=$(kubectl get configmap "$STATE_CM" -n "$NAMESPACE" -o jsonpath='{.data.last-sha}' 2>/dev/null || echo "")

    if [ "$SHA" != "$STORED_SHA" ]; then
      echo "New commit $SHA (was $STORED_SHA), updating state and triggering workflow"

      # Обновляем ConfigMap с новым SHA
      kubectl patch configmap "$STATE_CM" -n "$NAMESPACE" --type merge -p "{\"data\":{\"last-sha\":\"$SHA\"}}"

      # Триггерим Workflow из WorkflowTemplate backend-build
      cat <<EOF | kubectl apply -f -
      apiVersion: argoproj.io/v1alpha1
      kind: Workflow
      metadata:
        generateName: backend-build-
        namespace: ${namespace}
      spec:
        workflowTemplateRef:
          name: backend-build
        arguments:
          parameters:
            - name: git_sha
              value: "$SHA"
      EOF
    else
      echo "No changes - skip"
    fi
