// Altclaw Service Worker for Web Push Notifications
self.addEventListener('push', function(event) {
  if (!event.data) return;

  let data;
  try {
    data = event.data.json();
  } catch (e) {
    data = { title: 'Altclaw', body: event.data.text() };
  }

  const options = {
    body: data.body || '',
    icon: '/app/favicon.ico',
    badge: '/app/favicon.ico',
    data: { url: data.url || '/app/' },
    tag: 'altclaw-notification',
    renotify: true
  };

  event.waitUntil(
    self.registration.showNotification(data.title || 'Altclaw', options)
  );
});

self.addEventListener('notificationclick', function(event) {
  event.notification.close();

  const url = event.notification.data && event.notification.data.url
    ? event.notification.data.url
    : '/app/';

  event.waitUntil(
    clients.matchAll({ type: 'window', includeUncontrolled: true }).then(function(clientList) {
      // Focus existing window if found
      for (var i = 0; i < clientList.length; i++) {
        var client = clientList[i];
        if (client.url.includes('/app/') && 'focus' in client) {
          return client.focus();
        }
      }
      // Otherwise open a new window
      if (clients.openWindow) {
        return clients.openWindow(url);
      }
    })
  );
});
