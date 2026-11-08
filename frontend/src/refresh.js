const BACKEND = import.meta.env.VITE_BACKEND_URL

let refreshPromise = null

// Share one in-flight refresh between concurrent requests. Refresh tokens are
// often single-use, so several parallel refreshes could invalidate each other.
function refreshSession() {
    if (!refreshPromise) {
        refreshPromise = fetch(`${BACKEND}/refresh`, { method: "POST", credentials: "include" })
            .then(r => r.ok)
            .catch(() => false)
            .finally(() => { refreshPromise = null })
    }
    return refreshPromise
}

export async function apiFetch(path, options = {}) {
    const opts = { ...options, credentials: "include" }

    let response = await fetch(`${BACKEND}${path}`, opts)
    if (response.status === 401 && await refreshSession()) {
        response = await fetch(`${BACKEND}${path}`, opts) // retry once with the new token
    }
    return response
}