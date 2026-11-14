// The phone's notifications through Firebase (lib/app/notify.dart's
// PushPlatform): started from the server's values, kept on Android for when
// the phone starts the app for a notification (TaskiemApplication.kt).

import 'dart:async';
import 'dart:io' show Platform;

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/services.dart';

import '../api/api.g.dart';
import 'notify.dart';

class FirebasePush implements PushPlatform {
  static const _native = MethodChannel('taskiem/notify');

  bool _started = false;

  FirebaseMessaging get _m => FirebaseMessaging.instance;

  @override
  String get platform => Platform.isIOS ? 'ios' : 'android';

  @override
  Future<bool> start(AppConfigPushFcm v) async {
    if (_started) return true;
    final appId = Platform.isIOS ? v.appIdIos : v.appIdAndroid;
    if (appId == null || appId.isEmpty) return false;
    try {
      if (Platform.isAndroid) {
        await _native.invokeMethod<void>('keep', {
          'api_key': v.apiKey,
          'app_id': appId,
          'project_id': v.projectId,
          'sender_id': v.senderId,
        });
      }
      if (Firebase.apps.isEmpty) {
        try {
          await Firebase.initializeApp(
            options: FirebaseOptions(apiKey: v.apiKey, appId: appId, messagingSenderId: v.senderId, projectId: v.projectId),
          );
        } on FirebaseException catch (e) {
          // Started already at the app's start, from the values kept then
          // (the server's changed values take over at the next start).
          if (e.code != 'duplicate-app') rethrow;
        }
      }
      // An iPhone shows nothing itself while the app is in front, as Android does.
      await _m.setForegroundNotificationPresentationOptions(alert: false, badge: true, sound: false);
      _started = true;
      return true;
    } on Object {
      return false;
    }
  }

  // Android 13 and later says denied while it can still ask again (it stops
  // asking after the second no: deniedPermanently); an iPhone's denied is final.
  static PushPermission _of(AuthorizationStatus s) => switch (s) {
    AuthorizationStatus.authorized || AuthorizationStatus.provisional => PushPermission.granted,
    AuthorizationStatus.deniedPermanently => PushPermission.denied,
    AuthorizationStatus.denied => Platform.isIOS ? PushPermission.denied : PushPermission.notAsked,
    AuthorizationStatus.notDetermined => PushPermission.notAsked,
  };

  @override
  Future<PushPermission> permission() async {
    try {
      return _of((await _m.getNotificationSettings()).authorizationStatus);
    } on Object {
      return PushPermission.notAsked;
    }
  }

  @override
  Future<PushPermission> ask() async {
    try {
      return _of((await _m.requestPermission()).authorizationStatus);
    } on Object {
      return PushPermission.denied;
    }
  }

  @override
  Future<String?> token() async {
    try {
      // An iPhone has a token only once Apple has given the app its own.
      if (Platform.isIOS) {
        for (var i = 0; i < 10 && await _m.getAPNSToken() == null; i++) {
          await Future<void>.delayed(const Duration(milliseconds: 500));
        }
      }
      // The registration token FCM's HTTP v1 sends to (the server's
      // push.ValidToken); its newer installation ids are not taken there yet.
      // ignore: deprecated_member_use
      return await _m.getToken();
    } on Object {
      return null;
    }
  }

  @override
  // ignore: deprecated_member_use
  Stream<String> get tokenChanged => _m.onTokenRefresh;

  static Map<String, String> _data(RemoteMessage m) => {for (final e in m.data.entries) e.key: '${e.value}'};

  @override
  Future<Map<String, String>?> openedFrom() async {
    try {
      final m = await _m.getInitialMessage();
      return m == null ? null : _data(m);
    } on Object {
      return null;
    }
  }

  @override
  Stream<Map<String, String>> get opened => FirebaseMessaging.onMessageOpenedApp.map(_data);

  @override
  Stream<PushNote> get arrived => FirebaseMessaging.onMessage.map(
    (m) => PushNote(title: m.notification?.title ?? 'Taskiem', body: m.notification?.body ?? '', data: _data(m)),
  );

  @override
  Future<void> forget() async {
    // ignore: deprecated_member_use
    if (_started) await _m.deleteToken();
  }

  @override
  Future<void> unkeep() async {
    try {
      if (Platform.isAndroid) await _native.invokeMethod<void>('keep', null);
    } on Object {
      // Kept: Firebase starts with it and nothing comes.
    }
  }
}
