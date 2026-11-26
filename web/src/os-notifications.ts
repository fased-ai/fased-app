const OS_NOTIFY_PREF_KEY = "fased.os-notifications.enabled.v1";
export const OS_NOTIFY_PREF_EVENT = "fased:os-notify-pref";
const deliveredIds = new Set<string>();
const MAX_DELIVERED_TRACKED = 200;

export type OSNotificationPermission = NotificationPermission | "unsupported";

export function isOSNotificationSupported(): boolean {
  return (
    typeof window !== "undefined" && typeof window.Notification === "function"
  );
}

export function getOSNotificationPermission(): OSNotificationPermission {
  if (!isOSNotificationSupported()) return "unsupported";
  return window.Notification.permission;
}

export function getOSNotificationsEnabled(): boolean {
  try {
    return localStorage.getItem(OS_NOTIFY_PREF_KEY) === "true";
  } catch {
    return false;
  }
}

export function setOSNotificationsEnabled(enabled: boolean): void {
  try {
    localStorage.setItem(OS_NOTIFY_PREF_KEY, enabled ? "true" : "false");
    if (typeof window !== "undefined") {
      window.dispatchEvent(new CustomEvent(OS_NOTIFY_PREF_EVENT));
    }
  } catch {}
}

export async function requestOSNotificationPermission(): Promise<OSNotificationPermission> {
  if (!isOSNotificationSupported()) return "unsupported";
  try {
    const result = await window.Notification.requestPermission();
    return result;
  } catch {
    return getOSNotificationPermission();
  }
}

export function dispatchOSNotification(
  id: string,
  title: string,
  body?: string,
  force = false,
): boolean {
  if (!isOSNotificationSupported()) return false;
  if (!force && !getOSNotificationsEnabled()) return false;
  if (window.Notification.permission !== "granted") return false;
  const key = id.trim();
  if (key && !force) {
    if (deliveredIds.has(key)) return false;
    if (deliveredIds.size >= MAX_DELIVERED_TRACKED) {
      const oldest = deliveredIds.values().next().value;
      if (oldest !== undefined) deliveredIds.delete(oldest);
    }
    deliveredIds.add(key);
  }
  try {
    new window.Notification(title, {
      body: body || undefined,
      tag: key || undefined,
    });
    return true;
  } catch {
    return false;
  }
}
