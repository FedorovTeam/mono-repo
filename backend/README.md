Проблема:
Сейчас консоль доступен из веба, все пользователи которые имеют ссылку могут загружать то что вздумается - это не бозопасно

Решение:
Добавить авторизацию через сервис authentik

Client id: L6AmiswYzea2gAs5aGfzhLP9jlapG42ppPhtutoF
Client secret: lGhLYG9Usi845ndGd7R8yEVuauxMKZQYhbBVmcuaPAbewC9pUvQPH5uhSiuSGKWq8nYydBoUDNnbngr38nxiecE1qgeoO7e4nrh3carhwG89a3xoHXENDa1XIfh3sUfP
Callback URL: https://console.fedorov-team.tech/auth/sso/authentik
URI-s:
URL-адрес конфигурации OpenID
https://auth.tech.fedorov-team.tech/application/o/console/.well-known/openid-configuration
Эмитент конфигурации OpenID
https://auth.tech.fedorov-team.tech/application/o/console/
URL-адрес авторизации
https://auth.tech.fedorov-team.tech/application/o/authorize/
URL-адрес токена
https://auth.tech.fedorov-team.tech/application/o/token/
URL-адрес информации о пользователе
https://auth.tech.fedorov-team.tech/application/o/userinfo/
URL-адрес выхода из системы
https://auth.tech.fedorov-team.tech/application/o/console/end-session/
URL-адрес JWKS
https://auth.tech.fedorov-team.tech/application/o/console/jwks/

Как я это вижу:
- Как пользователь открывает консоль, если пользователь не авторизован - показывается кнопка авторизоваться
- При нажатии кнопку авторизации перенаправляет на authentik для авторизации
- Если пользователь авторизован открывается консоль

На верху страницы в NavBar имеется кнопка выйти - с правого края

Библиотеки для UI компонентов:
- https://github.com/radix-ui/primitives
- https://github.com/shadcn-ui/ui
- tailwind

как лучше реализовать авторизацию в console через authentik
Как это лучше реализовать
Спроектируй пожалуйста архитектуру