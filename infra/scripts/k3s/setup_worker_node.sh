# На мастере получить токен
# cat /var/lib/rancher/k3s/server/node-token
# K106d1dca267ca017739786c168cbebd6292c802c8ce84f153f9d74d886083b01a5::server:01368eb974787512c26dc30c3df9f403
# 91.132.57.227

# Установить воркер ноду
curl -sfL https://get.k3s.io | \
K3S_URL=https://<MASTER_NODE_IP>:6443 \
K3S_TOKEN="<K3S_TOKEN>" \
INSTALL_K3S_VERSION="v1.29.3+k3s1" \
sh -
