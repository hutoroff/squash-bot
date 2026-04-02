import { useEffect, useRef } from 'react'

interface LoginProps {
  botName: string
}

export default function Login({ botName }: LoginProps) {
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!botName || !containerRef.current) return
    const container = containerRef.current

    const script = document.createElement('script')
    script.src = 'https://telegram.org/js/telegram-widget.js?22'
    script.setAttribute('data-telegram-login', botName)
    script.setAttribute('data-size', 'large')
    script.setAttribute('data-auth-url', '/api/auth/callback')
    script.setAttribute('data-request-access', 'write')
    script.async = true
    container.appendChild(script)

    return () => {
      if (container.contains(script)) {
        container.removeChild(script)
      }
    }
  }, [botName])

  return (
    <div className="login-page">
      <div className="login-card">
        <h1>Squash Bot</h1>
        <p>Sign in with your Telegram account to manage your squash games.</p>
        <div ref={containerRef} className="telegram-login-widget" />
      </div>
    </div>
  )
}
