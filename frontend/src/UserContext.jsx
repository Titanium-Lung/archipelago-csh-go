import { createContext, useContext, useState, useEffect } from "react"
import { apiFetch } from "./refresh"

const UserContext = createContext(null)

export function UserProvider({ children }) {
    const [user, setUser] = useState(null)

    useEffect(() => {
        async function fetchUser() {
            const response = await apiFetch("/user")
            if (response.ok) {
                const data = await response.json()
                setUser(data)
            } else if (window.location.pathname !== '/login') {
                const next = window.location.pathname + window.location.search
                window.location.href = `/login?next=${encodeURIComponent(next)}`
            }
        }
        fetchUser()
    }, [])

    return (
        <UserContext.Provider value={user}>
            {children}
        </UserContext.Provider>
    )

}

export function useUser() {
    return useContext(UserContext)
}