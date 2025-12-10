```shell
docker login https://harbor.tech.fedorov-team.tech/ -u robot$argo-cd -p TPhohsjKohdEAprPzyba0PGVM8LH3b8r
```


```shell
kubectl create secret generic regcred --from-file=.dockerconfigjson=~/.docker/config.json --type=kubernetes.io/dockerconfigjson -n argo
```