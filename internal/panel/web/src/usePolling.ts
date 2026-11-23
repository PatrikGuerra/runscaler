import { useCallback, useEffect, useState } from 'react'

// usePolling loads data now and then every intervalMs (0 = only on reload()).
export function usePolling<T>(load: () => Promise<T>, intervalMs: number) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const reload = useCallback(() => {
    load().then(
      (d) => {
        setData(d)
        setError('')
      },
      (e: Error) => setError(e.message),
    )
  }, [load])
  useEffect(() => {
    reload()
    if (!intervalMs) return
    const id = setInterval(reload, intervalMs)
    return () => clearInterval(id)
  }, [reload, intervalMs])
  return { data, error, reload }
}
