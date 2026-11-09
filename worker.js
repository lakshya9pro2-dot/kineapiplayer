/**
 * Cloudflare Worker for Kineflex Web Player
 * Supports:
 *   - /movie/:id
 *   - /tv/:id/:season/:episode
 *   - /play=:videourl
 */

const TOKEN = "kine_live_9AuUd4Y1uqQ-GGACHQGNBIxn2E4oxm7H";

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
          "Access-Control-Allow-Headers": "Content-Type, Authorization, Range",
        },
      });
    }

    let media = null;

    // 1. Direct Play: /play=...
    if (pathname.startsWith("/play=")) {
      const directUrl = decodeURIComponent(pathname.substring(6));
      media = {
        id: "direct",
        title: url.searchParams.get("title") || "Online Video Player",
        url: directUrl,
        type: "direct",
      };
    } else if (url.searchParams.get("play")) {
      media = {
        id: "direct",
        title: url.searchParams.get("title") || "Online Video Player",
        url: url.searchParams.get("play"),
        type: "direct",
      };
    }

    // 2. Movie: /movie/:id or /api/movie/:id
    const movieMatch = pathname.match(/^\/(?:api\/)?movie\/([a-zA-Z0-9_-]+)/i);
    if (!media && movieMatch) {
      const id = movieMatch[1];
      try {
        let res = await fetch(`https://api.kineflex.site/v1/movie/${id}?web`, {
          headers: { Authorization: `Bearer ${TOKEN}`, Accept: "application/json" },
        });
        if (!res.ok) {
          res = await fetch(`https://api.kineflex.site/v1/tv/movie/${id}?web`, {
            headers: { Authorization: `Bearer ${TOKEN}`, Accept: "application/json" },
          });
        }
        if (res.ok) {
          const json = await res.json();
          if (json && json.data) {
            media = {
              id: String(json.data.id || id),
              title: json.data.name || "Movie",
              url: json.data.url,
              poster: json.data.poster || "",
              type: "movie",
            };
          }
        }
      } catch (err) {
        return new Response(`Error fetching movie: ${err.message}`, { status: 502 });
      }
    }

    // 3. TV: /tv/:id/:season/:episode or /api/tv/:id/:season/:episode
    const tvMatch = pathname.match(/^\/(?:api\/)?tv\/([a-zA-Z0-9_-]+)\/([0-9]+)\/([0-9]+)/i);
    if (!media && tvMatch) {
      const [, id, season, episode] = tvMatch;
      try {
        const res = await fetch(`https://api.kineflex.site/v1/tv/${id}/${season}/${episode}?web`, {
          headers: { Authorization: `Bearer ${TOKEN}`, Accept: "application/json" },
        });
        if (res.ok) {
          const json = await res.json();
          if (json && json.data) {
            media = {
              id: String(json.data.id || id),
              title: `${json.data.name || "Series"} S${season}E${episode}`,
              url: json.data.url,
              poster: json.data.poster || "",
              type: "tv",
              season: parseInt(season, 10),
              episode: parseInt(episode, 10),
            };
          }
        }
      } catch (err) {
        return new Response(`Error fetching TV show: ${err.message}`, { status: 502 });
      }
    }

    // Return JSON if requested
    const accept = request.headers.get("Accept") || "";
    if (media && (accept.includes("application/json") || url.searchParams.get("format") === "json")) {
      return new Response(JSON.stringify(media), {
        headers: {
          "Content-Type": "application/json",
          "Access-Control-Allow-Origin": "*",
        },
      });
    }

    // Otherwise serve player.html with injected media
    // If deployed with static asset binding, fetch asset; otherwise serve inline or fallback
    return fetch(request);
  },
};
