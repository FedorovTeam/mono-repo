Prot forwarding:
```shell
kubectl port-forward -n postgres svc/postgres-postgresql 5432:5432
```

# Сначала нужно установить пароль для Postgres
```shell
kubectl create secret generic postgres-secret \
  --from-literal=postgres-password=3&!tAL5bf38yfx \
  -n postgres
```

```shell
kubectl get pods -n postgres
kubectl run psql-client --rm -it --restart=Never \
  --namespace postgres \
  --image=bitnami/postgresql:15 \
  --env="PGPASSWORD=3&!tAL5bf38yfx" \
  -- psql -h postgresql -U postgres -d app

```