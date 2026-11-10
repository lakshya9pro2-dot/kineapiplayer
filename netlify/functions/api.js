/**
 * Netlify Serverless Backend Function for Kineflex Player
 * Routes all API calls server-side so client never talks directly to api.kineflex.site
 */

function resolveKey(rawKey) {
  if (!rawKey) return "";
  rawKey = rawKey.trim();
  if (rawKey.startsWith("kine_")) return rawKey;

  try {
    const decoded = Buffer.from(rawKey, "base64").toString("utf-8");
    if (/^[\x20-\x7E]+$/.test(decoded)) {
      return decoded;
    }
  } catch (e) {}

  return rawKey;
}

function extractKey(event) {
  const q = event.queryStringParameters || {};
  const qKey = q.key || q.api_key || q.token;
  if (qKey) return resolveKey(qKey);

  const headers = event.headers || {};
  const authHeader = headers.authorization || headers.Authorization || "";
  if (authHeader.toLowerCase().startsWith("bearer ")) {
    return resolveKey(authHeader.substring(7));
  }

  const xApiKey = headers["x-api-key"] || headers["X-API-Key"];
  if (xApiKey) return resolveKey(xApiKey);

  if (process.env.KINEFLEX_TOKEN) {
    return resolveKey(process.env.KINEFLEX_TOKEN);
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

const CORS_HEADERS = {
  "Access-Control-Allow-Origin": "*",
  "Access-Control-Allow-Methods": "GET, POST, OPTIONS, HEAD",
  "Access-Control-Allow-Headers": "Content-Type, Authorization, X-API-Key, Range",
  "Content-Type": "application/json",
};

exports.handler = async function (event) {
  // CORS Preflight
  if (event.httpMethod === "OPTIONS") {
    return {
      statusCode: 200,
      headers: CORS_HEADERS,
      body: "",
    };
  }

  // Normalize path
  let path = event.path || "/";
  // Remove Netlify function prefix if present
  path = path.replace(/^\/\.netlify\/functions\/api/, "");
  // Ensure starts with "/"
  if (!path.startsWith("/")) path = "/" + path;

  const key = extractKey(event);

  // 1. Account balance check
  if (path === "/account" || path === "/v1/account" || path === "/api/account") {
    if (!key) {
      return {
        statusCode: 401,
        headers: CORS_HEADERS,
        body: JSON.stringify({ success: false, error: makeMissingKeyError() }),
      };
    }
    try {
      const res = await fetch("https://api.kineflex.site/v1/account", {
        headers: { Authorization: `Bearer ${key}`, Accept: "application/json" },
      });
      const data = await res.text();
      return {
        statusCode: res.status,
        headers: CORS_HEADERS,
        body: data,
      };
    } catch (err) {
      return {
        statusCode: 502,
        headers: CORS_HEADERS,
        body: JSON.stringify({ success: false, error: { code: "UPSTREAM_ERROR", message: err.message, httpStatus: 502 } }),
      };
    }
  }

  // 2. Public endpoints
  if (path === "/packages" || path === "/v1/packages") {
    const res = await fetch("https://api.kineflex.site/v1/packages");
    const data = await res.text();
    return { statusCode: res.status, headers: CORS_HEADERS, body: data };
  }
  if (path === "/health") {
    const res = await fetch("https://api.kineflex.site/health");
    const data = await res.text();
    return { statusCode: res.status, headers: CORS_HEADERS, body: data };
  }
  if (path === "/version") {
    const res = await fetch("https://api.kineflex.site/version");
    const data = await res.text();
    return { statusCode: res.status, headers: CORS_HEADERS, body: data };
  }

  // 3. Movie routes: /(api/)?(movie|move)/:id
  const movieMatch = path.match(/^\/(?:api\/)?(?:movie|move)\/([a-zA-Z0-9_-]+)/i);
  if (movieMatch) {
    const id = movieMatch[1];
    if (!key) {
      return {
        statusCode: 401,
        headers: CORS_HEADERS,
        body: JSON.stringify({ success: false, error: makeMissingKeyError() }),
      };
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
        return {
          statusCode: 200,
          headers: CORS_HEADERS,
          body: JSON.stringify({
            success: true,
            data: {
              id: String(json.data.id || id),
              name: json.data.name || "Movie",
              title: json.data.name || "Movie",
              url: json.data.url,
              poster: json.data.poster || "",
              type: "movie",
            },
          }),
        };
      } else {
        return {
          statusCode: res.status,
          headers: CORS_HEADERS,
          body: JSON.stringify({
            success: false,
            error: {
              code: json?.error?.code || (res.status === 404 ? "NOT_FOUND" : res.status === 502 ? "UPSTREAM_ERROR" : "API_ERROR"),
              message: json?.error?.message || json?.message || "Video not found or upstream error",
              httpStatus: res.status,
              required: json?.error?.required,
              remaining: json?.error?.remaining,
            },
          }),
        };
      }
    } catch (err) {
      return {
        statusCode: 502,
        headers: CORS_HEADERS,
        body: JSON.stringify({
          success: false,
          error: { code: "UPSTREAM_ERROR", message: err.message, httpStatus: 502 },
        }),
      };
    }
  }

  // 4. TV routes: /(api/)?tv/:id/:season/:episode
  const tvMatch = path.match(/^\/(?:api\/)?tv\/([a-zA-Z0-9_-]+)\/([0-9]+)\/([0-9]+)/i);
  if (tvMatch) {
    const [, id, season, episode] = tvMatch;
    if (!key) {
      return {
        statusCode: 401,
        headers: CORS_HEADERS,
        body: JSON.stringify({ success: false, error: makeMissingKeyError() }),
      };
    }

    try {
      const res = await fetch(`https://api.kineflex.site/v1/tv/${id}/${season}/${episode}?web`, {
        headers: { Authorization: `Bearer ${key}`, Accept: "application/json" },
      });

      const json = await res.json().catch(() => null);
      if (res.ok && json && json.data && json.data.url) {
        return {
          statusCode: 200,
          headers: CORS_HEADERS,
          body: JSON.stringify({
            success: true,
            data: {
              id: String(json.data.id || id),
              name: json.data.name || "Series",
              title: `${json.data.name || "Series"} S${season}E${episode}`,
              url: json.data.url,
              poster: json.data.poster || "",
              type: "tv",
              season: parseInt(season, 10),
              episode: parseInt(episode, 10),
            },
          }),
        };
      } else {
        return {
          statusCode: res.status,
          headers: CORS_HEADERS,
          body: JSON.stringify({
            success: false,
            error: {
              code: json?.error?.code || (res.status === 404 ? "NOT_FOUND" : res.status === 502 ? "UPSTREAM_ERROR" : "API_ERROR"),
              message: json?.error?.message || json?.message || "Episode not found or upstream error",
              httpStatus: res.status,
              required: json?.error?.required,
              remaining: json?.error?.remaining,
            },
          }),
        };
      }
    } catch (err) {
      return {
        statusCode: 502,
        headers: CORS_HEADERS,
        body: JSON.stringify({
          success: false,
          error: { code: "UPSTREAM_ERROR", message: err.message, httpStatus: 502 },
        }),
      };
    }
  }

  return {
    statusCode: 404,
    headers: CORS_HEADERS,
    body: JSON.stringify({ success: false, error: { code: "NOT_FOUND", message: "Endpoint not found" } }),
  };
};
