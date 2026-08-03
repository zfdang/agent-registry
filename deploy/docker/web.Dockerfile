# Stage 1 — build the static assets.
FROM node:22-alpine AS build

WORKDIR /app

# Install deps first so this layer is cached across source-only changes.
COPY web/package.json web/pnpm-lock.yaml ./
# Pinned (not "latest") so the container resolves the same pnpm behavior
# as local dev — a newer pnpm enforces a supply-chain minimum-release-age
# policy that can reject freshly-published transitive deps out of our control.
RUN corepack enable && corepack prepare pnpm@10.12.1 --activate \
    && pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build

# Stage 2 — serve dist/ with an unprivileged nginx.
FROM nginxinc/nginx-unprivileged:alpine

COPY --from=build /app/dist /usr/share/nginx/html
COPY web/nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 3000
