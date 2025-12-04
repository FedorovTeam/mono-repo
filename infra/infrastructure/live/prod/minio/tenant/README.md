# Minio Tenant

This directory deploys the Minio tenant using Terragrunt.

Set the root credentials via environment variables before applying:

```bash
export MINIO_ROOT_USER="Burdensome3253"
export MINIO_ROOT_PASSWORD="1545Upz_9jwI58__"
```

Then run:

```bash
terragrunt apply
```

The module creates a Kubernetes secret with these credentials automatically.

```shell
kubectl delete serverstransport.traefik.io -n minio minio-console-transport
kubectl delete secret -n minio minio-tenant-prod-creds
```