Install MinIO Operator with Console via Terragrunt:

```bash
cd infrastructure/live/prod/minio/operator
kubectl annotate crd policybindings.sts.min.io \
  meta.helm.sh/release-name=minio-operator \
  meta.helm.sh/release-namespace=minio-operator \
  --overwrite

terragrunt apply

kubectl apply -f operator-console-certificate.yaml
```
