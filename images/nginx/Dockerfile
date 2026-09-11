FROM nginx:latest

USER root

# 公式イメージの worker は通常 nginx ユーザーになるため、root ユーザーで起動するようにする
RUN sed -i 's/^user  nginx;/user  root;/' /etc/nginx/nginx.conf

# サンプルへ接続したことが分かるように、タイトルとページの見出しを変更する
RUN sed -i \
	-e 's#<title>Welcome to nginx!</title>#<title>toy-kubernetes nginx</title>#' \
	-e 's#<h1>Welcome to nginx!</h1>#<h1>Hello from toy-kubernetes!</h1>#' \
	/usr/share/nginx/html/index.html
