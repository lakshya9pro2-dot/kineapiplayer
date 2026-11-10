/**
 * Cloudflare Worker for Kineflex Web Player
 * Supports:
 *   - /movie/:id?key=<api_key_or_base64>
 *   - /tv/:id/:season/:episode?key=<api_key_or_base64>
 *   - /play=:videourl
 *   - /v1/account
 */

function resolveKey(rawKey) {
  if (!rawKey) return "";
  rawKey = rawKey.trim();
  if (rawKey.startsWith("kine_")) return rawKey;

  try {
    const decoded = atob(rawKey);
    if (/^[\x20-\x7E]+$/.test(decoded)) {
      return decoded;
    }
  } catch (e) {}

  return rawKey;
}

function extractKey(request, env) {
  const url = new URL(request.url);
  const qKey = url.searchParams.get("key") || url.searchParams.get("api_key") || url.searchParams.get("token");
  if (qKey) return resolveKey(qKey);

  const authHeader = request.headers.get("Authorization") || "";
  if (authHeader.toLowerCase().startsWith("bearer ")) {
    return resolveKey(authHeader.substring(7));
  }

  const xApiKey = request.headers.get("X-API-Key");
  if (xApiKey) return resolveKey(xApiKey);

  if (env && env.KINEFLEX_TOKEN) {
    return resolveKey(env.KINEFLEX_TOKEN);
  }

  return "";
}

function makeMissingKeyError() {
  return {
    code: "MISSING_API_KEY",
    message: "No API key was provided. Get your free API key at api.kineflex.site (free users get 100 points). If you are a developer, contact admin on Telegram: @lakshya9pro.",
    httpStatus: 401,
    keyProvided: false,
  };
}

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    const pathname = url.pathname;

    // Handle CORS preflight
    if (request.method === "OPTIONS") {
      return new Response(null, {
        headers: {
          "Access-Control-Allow-Origin": "*",
          "Access-Control-Allow-Methods": "GET, POST, OPTIONS, HEAD",
          "Access-Control-Allow-Headers": "Content-Type, Authorization, X-API-Key, Range",
        },
      });
    }

    const key = extractKey(request, env);
    const wantsJSON =
      request.headers.get("Accept")?.includes("application/json") ||
      url.searchParams.get("format") === "json" ||
      url.searchParams.get("json") === "true";

    // 0. Auto-redirect /move/* to /movie/*
    if (pathname.startsWith("/move/")) {
      url.pathname = pathname.replace(/^\/move\//i, "/movie/");
      return Response.redirect(url.toString(), 301);
    }

    // 1. Cloudflare Workers Static Assets & Pages support
    if (env && env.ASSETS) {
      if (!wantsJSON && !pathname.startsWith("/api/")) {
        // Rewrite movie/tv routes to player.html (keeps URL clean in browser)
        if (pathname.match(/^\/(?:movie|move)\//i) || pathname.match(/^\/tv\//i) || pathname.startsWith("/play=")) {
          return env.ASSETS.fetch(new URL("/player.html", request.url));
        }
        // Root path serves index.html
        if (pathname === "/" || pathname === "") {
          return env.ASSETS.fetch(new URL("/index.html", request.url));
        }
      }
      // Check static assets (posters, icons, json, images)
      const assetRes = await env.ASSETS.fetch(request);
      if (assetRes.status !== 404) {
        return assetRes;
      }
    }

    // 2. Optional reverse proxy origin support (e.g., Netlify origin)
    if (env && env.ORIGIN_URL) {
      const origin = env.ORIGIN_URL.replace(/\/$/, "");
      if (!wantsJSON && !pathname.startsWith("/api/")) {
        if (pathname.match(/^\/(?:movie|move)\//i) || pathname.match(/^\/tv\//i) || pathname.startsWith("/play=")) {
          return fetch(`${origin}/player.html`, request);
        }
        if (pathname === "/" || pathname === "") {
          return fetch(`${origin}/index.html`, request);
        }
      }
    }

    // 3. Direct Play: /play=...
    if (pathname.startsWith("/play=")) {
      const directUrl = decodeURIComponent(pathname.substring(6));
      const payload = {
        id: "direct",
        title: url.searchParams.get("title") || "Online Video Player",
        url: directUrl,
        type: "direct",
      };
      if (wantsJSON) {
        return new Response(JSON.stringify(payload), {
          headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
        });
      }
      return fetch(request);
    } else if (url.searchParams.get("play")) {
      const payload = {
        id: "direct",
        title: url.searchParams.get("title") || "Online Video Player",
        url: url.searchParams.get("play"),
        type: "direct",
      };
      if (wantsJSON) {
        return new Response(JSON.stringify(payload), {
          headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
        });
      }
      return fetch(request);
    }

    // 2. Account balance check: /v1/account or /api/account
    if (pathname === "/v1/account" || pathname === "/api/account") {
      if (!key) {
        return new Response(JSON.stringify({ success: false, error: makeMissingKeyError() }), {
          status: 401,
          headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
        });
      }
      const accRes = await fetch("https://api.kineflex.site/v1/account", {
        headers: { Authorization: `Bearer ${key}`, Accept: "application/json" },
      });
      return new Response(accRes.body, {
        status: accRes.status,
        headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
      });
    }

    // 3. Public endpoints
    if (pathname === "/v1/packages" || pathname === "/api/packages") {
      const res = await fetch("https://api.kineflex.site/v1/packages");
      return new Response(res.body, { status: res.status, headers: { "Content-Type": "application/json" } });
    }
    if (pathname === "/health" || pathname === "/api/health") {
      const res = await fetch("https://api.kineflex.site/health");
      return new Response(res.body, { status: res.status, headers: { "Content-Type": "application/json" } });
    }
    if (pathname === "/version" || pathname === "/api/version") {
      const res = await fetch("https://api.kineflex.site/version");
      return new Response(res.body, { status: res.status, headers: { "Content-Type": "application/json" } });
    }

    // 4. Movie: /movie/:id or /api/movie/:id
    const movieMatch = pathname.match(/^\/(?:api\/)?(?:movie|move)\/([a-zA-Z0-9_-]+)/i);
    if (movieMatch) {
      const id = movieMatch[1];
      if (!key) {
        const errPayload = makeMissingKeyError();
        if (wantsJSON || pathname.startsWith("/api/")) {
          return new Response(JSON.stringify({ success: false, error: errPayload }), {
            status: 401,
            headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
          });
        }
        return fetch(request);
      }

      try {
        let res = await fetch(`https://api.kineflex.site/v1/tv/movie/${id}?web`, {
          headers: { Authorization: `Bearer ${key}`, Accept: "application/json" },
        });
        if (!res.ok) {
          res = await fetch(`https://api.kineflex.site/v1/movie/${id}?web`, {
            headers: { Authorization: `Bearer ${key}`, Accept: "application/json" },
          });
        }

        const json = await res.json().catch(() => null);
        if (res.ok && json && json.data && json.data.url) {
          const media = {
            id: String(json.data.id || id),
            title: json.data.name || "Movie",
            url: json.data.url,
            poster: json.data.poster || "",
            type: "movie",
          };
          if (wantsJSON || pathname.startsWith("/api/")) {
            return new Response(JSON.stringify({ success: true, data: media }), {
              headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
            });
          }
        } else {
          const errPayload = {
            code: json?.error?.code || (res.status === 404 ? "NOT_FOUND" : res.status === 502 ? "UPSTREAM_ERROR" : "API_ERROR"),
            message: json?.error?.message || json?.message || "Video not found or upstream error",
            httpStatus: res.status,
            required: json?.error?.required,
            remaining: json?.error?.remaining,
          };
          if (wantsJSON || pathname.startsWith("/api/")) {
            return new Response(JSON.stringify({ success: false, error: errPayload }), {
              status: res.status,
              headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
            });
          }
        }
      } catch (err) {
        if (wantsJSON || pathname.startsWith("/api/")) {
          return new Response(JSON.stringify({ success: false, error: { code: "UPSTREAM_ERROR", message: err.message, httpStatus: 502 } }), {
            status: 502,
            headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
          });
        }
      }
    }

    // 5. TV: /tv/:id/:season/:episode or /api/tv/:id/:season/:episode
    const tvMatch = pathname.match(/^\/(?:api\/)?tv\/([a-zA-Z0-9_-]+)\/([0-9]+)\/([0-9]+)/i);
    if (tvMatch) {
      const [, id, season, episode] = tvMatch;
      if (!key) {
        const errPayload = makeMissingKeyError();
        if (wantsJSON || pathname.startsWith("/api/")) {
          return new Response(JSON.stringify({ success: false, error: errPayload }), {
            status: 401,
            headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
          });
        }
        return fetch(request);
      }

      try {
        const res = await fetch(`https://api.kineflex.site/v1/tv/${id}/${season}/${episode}?web`, {
          headers: { Authorization: `Bearer ${key}`, Accept: "application/json" },
        });

        const json = await res.json().catch(() => null);
        if (res.ok && json && json.data && json.data.url) {
          const media = {
            id: String(json.data.id || id),
            title: `${json.data.name || "Series"} S${season}E${episode}`,
            url: json.data.url,
            poster: json.data.poster || "",
            type: "tv",
            season: parseInt(season, 10),
            episode: parseInt(episode, 10),
          };
          if (wantsJSON || pathname.startsWith("/api/")) {
            return new Response(JSON.stringify({ success: true, data: media }), {
              headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
            });
          }
        } else {
          const errPayload = {
            code: json?.error?.code || (res.status === 404 ? "NOT_FOUND" : res.status === 502 ? "UPSTREAM_ERROR" : "API_ERROR"),
            message: json?.error?.message || json?.message || "Episode not found or upstream error",
            httpStatus: res.status,
            required: json?.error?.required,
            remaining: json?.error?.remaining,
          };
          if (wantsJSON || pathname.startsWith("/api/")) {
            return new Response(JSON.stringify({ success: false, error: errPayload }), {
              status: res.status,
              headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
            });
          }
        }
      } catch (err) {
        if (wantsJSON || pathname.startsWith("/api/")) {
          return new Response(JSON.stringify({ success: false, error: { code: "UPSTREAM_ERROR", message: err.message, httpStatus: 502 } }), {
            status: 502,
            headers: { "Content-Type": "application/json", "Access-Control-Allow-Origin": "*" },
          });
        }
      }
    }

    return fetch(request);
  },
};
