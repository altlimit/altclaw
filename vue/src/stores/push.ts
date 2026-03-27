// Push notification helpers for subscribing/unsubscribing to web push.

// URL-safe base64 decode (VAPID public key format)
function urlBase64ToUint8Array(base64String: string): Uint8Array {
  const padding = '='.repeat((4 - (base64String.length % 4)) % 4)
  const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/')
  const rawData = window.atob(base64)
  const outputArray = new Uint8Array(rawData.length)
  for (let i = 0; i < rawData.length; ++i) {
    outputArray[i] = rawData.charCodeAt(i)
  }
  return outputArray
}

export async function isPushSupported(): Promise<boolean> {
  return 'serviceWorker' in navigator && 'PushManager' in window && 'Notification' in window
}

export async function getPushPermission(): Promise<NotificationPermission> {
  return Notification.permission
}

export async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!('serviceWorker' in navigator)) return null
  try {
    return await navigator.serviceWorker.register('/app/sw.js', { scope: '/app/' })
  } catch (e) {
    console.error('SW registration failed:', e)
    return null
  }
}

export async function subscribeToPush(): Promise<boolean> {
  try {
    const reg = await registerServiceWorker()
    if (!reg) return false

    const permission = await Notification.requestPermission()
    if (permission !== 'granted') return false

    // Fetch VAPID public key from the server
    const resp = await fetch('/api/vapid-public-key')
    if (!resp.ok) return false
    const { public_key } = await resp.json()
    if (!public_key) return false

    const subscription = await reg.pushManager.subscribe({
      userVisibleOnly: true,
      applicationServerKey: urlBase64ToUint8Array(public_key).buffer as ArrayBuffer,
    })

    const json = subscription.toJSON()

    // Send subscription to the backend
    const saveResp = await fetch('/api/push-subscribe', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        endpoint: json.endpoint,
        p256dh: json.keys?.p256dh || '',
        auth: json.keys?.auth || '',
      }),
    })
    return saveResp.ok
  } catch (e) {
    console.error('Push subscribe failed:', e)
    return false
  }
}

export async function unsubscribeFromPush(): Promise<boolean> {
  try {
    const reg = await navigator.serviceWorker.getRegistration('/app/')
    if (!reg) return true

    const subscription = await reg.pushManager.getSubscription()
    if (!subscription) return true

    // Tell backend to remove
    await fetch('/api/push-unsubscribe', {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ endpoint: subscription.endpoint }),
    })

    await subscription.unsubscribe()
    return true
  } catch (e) {
    console.error('Push unsubscribe failed:', e)
    return false
  }
}

export async function isSubscribedToPush(): Promise<boolean> {
  try {
    const reg = await navigator.serviceWorker.getRegistration('/app/')
    if (!reg) return false
    const sub = await reg.pushManager.getSubscription()
    return !!sub
  } catch {
    return false
  }
}
