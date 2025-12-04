cd ../..

# Устанавливаем cert manager
cd infrastructure/live/prod/cert-manager
terragrunt apply

# Убедись, что CRD установлены
kubectl get crd | grep clusterissuers

cd ../cert-manager-bootstrap
terragrunt apply


# Устанавливаем traefik
cd ../traefik
kubectl delete ingressclass traefik
terragrunt apply
# kubectl get svc -n traefik

# Установить helm-чарт + CRD
cd infrastructure/live/prod/metallb-install
terragrunt apply

# Проверить, что CRD появились:
kubectl get crd | grep metallb

# Установить IPAddressPool и L2Advertisement
cd ../metallb
terragrunt apply

# Проверить:
kubectl get ipaddresspools.metallb.io -n metallb-system
kubectl get l2advertisements.metallb.io -n metallb-system

# Применяем тестовый ингрес контроллер
kubectl apply -f ../../../../manifests/whoami-ingress.yaml
