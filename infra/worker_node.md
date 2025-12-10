Установить воркер ноду:

```shell
curl -sfL https://get.k3s.io | \
K3S_URL=https://80.74.29.116:6443 \
K3S_TOKEN="K109561e876ad19856a28c88e6e301362384059e1b1c0ee6ba782b04f4093f03405::server:48fe095fdecde784cca78f7bd5560c78" \
INSTALL_K3S_VERSION="v1.29.3+k3s1" \
sh -
```