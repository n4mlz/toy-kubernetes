FROM nginx:latest

USER root

# 公式イメージの worker は通常 nginx ユーザーになるため、root ユーザーで起動するようにする
RUN sed -i 's/^user  nginx;/user  root;/' /etc/nginx/nginx.conf

# 文言をいい感じに置き換える
RUN sed -i 's#<h1>Welcome to nginx!</h1>#<h1>Hello from toy-kubernetes!</h1>#' /usr/share/nginx/html/index.html
