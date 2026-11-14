# Cohost production image: Next.js Node server. Build context is frontend/.
# next start (not standalone output) keeps next.config.ts identical to Vercel.
FROM node:22-alpine
WORKDIR /app
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build \
 && npm prune --omit=dev \
 && chown -R node:node /app
USER node
EXPOSE 3000
CMD ["npm", "run", "start"]
