Чтобы сервисы деплоились, нужно создать для них секреты

```shell
docker login https://harbor.tech.fedorov-team.tech/ -u robot$argo-cd -p TPhohsjKohdEAprPzyba0PGVM8LH3b8r
```


```shell
cat ~/.docker/config.json

create file dockerconfig.json

kubectl create secret generic regcred --from-file=.dockerconfigjson=./dockerconfig.json --type=kubernetes.io/dockerconfigjson -n argocd
```

```shell
kubectl delete secret regcred -n app-knowledge-console
```

kubectl get secret regcred -n argocd -o yaml \
  | sed 's/namespace: argocd/namespace: storage-service/' \
  | sed '/^\s*uid:/d' \
  | sed '/^\s*resourceVersion:/d' \
  | sed '/^\s*creationTimestamp:/d' \
  | sed '/^\s*managedFields:/,$d' \
  | kubectl apply -f -