"use client";

/**
 * Detects in-app browsers (WebViews) that Google OAuth blocks.
 * Google rejects OAuth in WebViews with error 403: disallowed_useragent.
 */
function isInAppBrowser(): boolean {
  if (typeof navigator === "undefined") return false;
  const ua = navigator.userAgent || "";
  // Common in-app browser indicators
  return /FBAN|FBAV|Instagram|Line\/|Twitter|Snapchat|WeChat|MicroMessenger|LinkedIn/i.test(ua);
}

export function SignInLink({
  className,
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  const handleClick = (e: React.MouseEvent) => {
    if (isInAppBrowser()) {
      e.preventDefault();
      // Try to open in system browser on iOS/Android
      const url = window.location.origin + "/api/auth/login";
      // iOS: window.open in in-app browsers sometimes opens Safari
      window.open(url, "_blank");
      // Also show a message in case the open didn't work
      alert(
        "Google Sign-In doesn't work in this browser. Please open e2a.dev in Safari or Chrome to sign in."
      );
    }
  };

  return (
    <a href="/api/auth/login" className={className} onClick={handleClick}>
      {children}
    </a>
  );
}
