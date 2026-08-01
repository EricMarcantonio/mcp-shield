# Build context is web/admin/, not the repo root:
#   docker build -t mcp-shield-admin ./web/admin
FROM node:24-alpine AS builder
WORKDIR /src

COPY package.json package-lock.json ./
RUN npm ci

COPY . .
# Left empty on purpose: the API base URL is a runtime concern, injected into
# /config.js by the entrypoint below, so one image works against any gateway.
ARG VITE_API_BASE_URL=""
ARG VITE_OPERATOR_NAME=""
ENV VITE_API_BASE_URL=$VITE_API_BASE_URL
ENV VITE_OPERATOR_NAME=$VITE_OPERATOR_NAME
RUN npm run build

FROM nginx:1.29-alpine
COPY --from=builder /src/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY docker-entrypoint.sh /docker-entrypoint.d/40-mcp-shield-config.sh
RUN chmod +x /docker-entrypoint.d/40-mcp-shield-config.sh

# API_BASE_URL    where the approval API is, as the browser will reach it.
#                 Empty means "same origin", which is what API_PROXY_PASS below
#                 is for.
# API_PROXY_PASS  when set, nginx proxies /api and /healthz to this upstream,
#                 so the browser stays same-origin and the gateway needs no
#                 CORS configuration. e.g. http://gateway:8081
# OPERATOR_NAME   fixes the name decisions are recorded under.
ENV API_BASE_URL=""
ENV OPERATOR_NAME=""
ENV API_PROXY_PASS=""

EXPOSE 80
