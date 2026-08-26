# CISync web — dev/demo image. Build: docker build -t cisync/web apps/web
FROM node:22-alpine AS build
WORKDIR /src
RUN corepack enable
COPY package.json ./
# pnpm v10 blocks dependency postinstall scripts unless approved; the dev
# image needs esbuild/sharp/unrs-resolver binaries, so allow them explicitly
# (non-interactive equivalent of `pnpm approve-builds`).
RUN pnpm install --no-frozen-lockfile --dangerously-allow-all-builds
COPY . .
RUN pnpm build

FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production
RUN corepack enable
COPY --from=build /src ./
EXPOSE 3000
CMD ["pnpm", "start"]
