```shell
docker build -t harbor.tech.fedorov-team.tech/backend/docling-serve-cpu:with-rus -f infra/images/docling-serve/Dockerfile .

docker push harbor.tech.fedorov-team.tech/backend/docling-serve-cpu:with-rus
```



```shell
root@a39306:~# kubectl get pods -n docling
NAME                             READY   STATUS    RESTARTS       AGE
docling-serve-596c78bcb4-78c6v   1/1     Running   54 (41m ago)   5d22h

root@a39306:~# kubectl -n docling exec -it deploy/docling-serve -- sh

((app-root) ) sh-5.1$ tesseract --list-langs
List of available languages (2):
eng
osd
```