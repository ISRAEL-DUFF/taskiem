// Where the app opens a browser (docs/spec/mobile-app.md §3.5): a page in
// the phone's own browser tab (Custom Tabs on Android, Safari's on an
// iPhone), over the app and back to it when closed. Only web addresses.

import 'package:url_launcher/url_launcher.dart';

/// Opens [url] in the browser tab; false when it is not a web address or
/// nothing could open it. A test stands in for it.
Future<bool> Function(Uri url) openInBrowser = (url) async {
  if (url.scheme != 'https' && url.scheme != 'http') return false;
  try {
    return await launchUrl(url, mode: LaunchMode.inAppBrowserView);
  } on Object {
    return false;
  }
};

/// Opens [url] in the app it belongs to (a t.me link in Telegram), or the
/// phone's browser; false when nothing could. A test stands in for it.
Future<bool> Function(Uri url) openOutside = (url) async {
  if (url.scheme != 'https' && url.scheme != 'http') return false;
  try {
    return await launchUrl(url, mode: LaunchMode.externalApplication);
  } on Object {
    return false;
  }
};
