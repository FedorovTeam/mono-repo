```shell
docker login https://harbor.tech.fedorov-team.tech/ -u robot$argo-cd -p TPhohsjKohdEAprPzyba0PGVM8LH3b8r
```


```shell
cat ~/.docker/config.json

create file dockerconfig.json

kubectl create secret generic regcred --from-file=.dockerconfigjson=./dockerconfig.json --type=kubernetes.io/dockerconfigjson -n argocd
```