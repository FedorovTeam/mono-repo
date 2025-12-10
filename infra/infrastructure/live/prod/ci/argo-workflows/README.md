```shell
kubectl create namespace argo
```

```shell
apt install jq
```

```shell
terragrunt apply \
  -target=helm_release.argo_workflows_crds \
  -target=helm_release.argo_workflows
```


```shell
terragrunt apply
```