// Where the app opens a browser (docs/spec/mobile-app.md §3.5): a page in
// the phone's own browser tab (Custom Tabs on Android, Safari's on an
// iPhone), over the app and back to it when closed. Only web addresses.

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_native_contact_picker/flutter_native_contact_picker.dart';
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

/// Rings [phone] from the phone's dialler (`tel:`); false when nothing
/// could. A test stands in for it.
Future<bool> Function(String phone) callPhone = (phone) async {
  try {
    return await launchUrl(Uri(scheme: 'tel', path: phone.replaceAll(RegExp(r'[^\d+]'), '')));
  } on Object {
    return false;
  }
};

/// A person picked from the phone's contacts, in the phone's own picker
/// (the app reads only the one picked); null when none was. A test stands
/// in for it.
Future<({String name, String phone})?> Function() pickContact = () async {
  try {
    final c = await FlutterNativeContactPicker().selectPhoneNumber();
    if (c == null) return null;
    return (name: c.fullName ?? '', phone: c.selectedPhoneNumber ?? (c.phoneNumbers?.firstOrNull ?? ''));
  } on Object {
    return null;
  }
};

/// Writes a text to [phone] from the phone's messages app (`sms:`); false
/// when nothing could. A test stands in for it.
Future<bool> Function(String phone) textPhone = (phone) async {
  try {
    return await launchUrl(Uri(scheme: 'sms', path: phone.replaceAll(RegExp(r'[^\d+]'), '')));
  } on Object {
    return false;
  }
};

/// Writes to [email] from the phone's mail app (`mailto:`); false when
/// nothing could. A test stands in for it.
Future<bool> Function(String email) mailTo = (email) async {
  try {
    return await launchUrl(Uri(scheme: 'mailto', path: email));
  } on Object {
    return false;
  }
};

/// Opens the phone's email app at its inbox (a password reset's link waits
/// there): Android's email app by MainActivity, the iPhone's Mail; false
/// when nothing could. A test stands in for it.
Future<bool> Function() openMailApp = () async {
  try {
    if (defaultTargetPlatform == TargetPlatform.android) {
      return await const MethodChannel('taskiem/window').invokeMethod<bool>('mail') ?? false;
    }
    return await launchUrl(Uri.parse('message://'));
  } on Object {
    return false;
  }
};
