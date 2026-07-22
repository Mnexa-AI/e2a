# syntax=docker/dockerfile:1
#
# Multi-stage build for @e2a/mcp-server's HTTP bin. The image runs the
# bin published as `node mcp/dist/bin/http.js`; the stdio bin is unused
# here but ships in the same artifact so we don't fork the build.

FROM node:22-alpine AS deps
WORKDIR /app
# Copy only manifests so this layer caches across code changes.
COPY package.json ./
COPY sdks/typescript/package.json sdks/typescript/
COPY mcp/package.json mcp/
COPY cli/package.json cli/
RUN npm install --package-lock=false --include=dev --workspaces --no-audit --no-fund

FROM node:22-alpine AS build
WORKDIR /app
# npm workspaces hoists all deps to the root node_modules.
COPY --from=deps /app/node_modules ./node_modules
COPY --from=deps /app/package.json ./
COPY --from=deps /app/sdks/typescript/package.json sdks/typescript/
COPY --from=deps /app/mcp/package.json mcp/
COPY --from=deps /app/cli/package.json cli/
COPY sdks/typescript ./sdks/typescript
COPY mcp ./mcp
RUN npm run build --workspace @e2a/sdk
RUN npm run build --workspace @e2a/mcp-server

FROM node:22-alpine AS runtime
WORKDIR /app
ARG MCP_SERVER_VERSION
ENV NODE_ENV=production
ENV MCP_SERVER_VERSION=${MCP_SERVER_VERSION}
# Fresh install with production-only deps so the runtime image is lean.
COPY --from=build /app/package.json ./
COPY --from=build /app/sdks/typescript/package.json sdks/typescript/
COPY --from=build /app/mcp/package.json mcp/
COPY --from=build /app/cli/package.json cli/
RUN npm install --package-lock=false --omit=dev --workspaces --no-audit --no-fund \
  && npm cache clean --force
# Built JS only — no TypeScript source ships in the image.
COPY --from=build /app/sdks/typescript/dist ./sdks/typescript/dist
COPY --from=build /app/mcp/dist ./mcp/dist
COPY VERSION ./VERSION
EXPOSE 3000
USER node
CMD ["node", "mcp/dist/bin/http.js"]
