// When the phone's camera or photos would not open for a photo (image
// picker): said in plain words, with the way to the phone's settings when
// Taskiem was refused.
//
// The app declares CAMERA (for the QR scanner; mobile_scanner merges it
// too). Because it is declared, Android's image picker asks for the camera
// before every photo taken and, refused, throws `camera_access_denied`;
// an iPhone throws the same once the camera is refused, and asks no more.
// Every place that takes or picks a photo catches it and says it here, so
// nothing fails silently.

import 'dart:async';

import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';
import 'package:geolocator/geolocator.dart' as geo;

import '../app/lock.dart' show stepOut;
import '../kit/modal.dart' show showTkToast;

/// Why a photo could not be taken or picked.
enum PickProblem {
  /// Taskiem is not allowed (the camera or the photos refused): only the
  /// phone's settings can change it once refused for good.
  refused,

  /// The phone has no camera Taskiem can use.
  noCamera,

  /// Anything else.
  failed,
}

/// The problem [e] (what the picker threw) is.
PickProblem pickProblemOf(Object e) {
  if (e is PlatformException) {
    final c = e.code.toLowerCase();
    if (c.contains('denied') || c.contains('access') || c.contains('permission')) return PickProblem.refused;
    if (c.contains('no_available_camera') || c.contains('no camera')) return PickProblem.noCamera;
  }
  return PickProblem.failed;
}

/// The words for [p]; [camera]: a photo taken now, else one chosen from
/// the phone's photos.
String pickProblemWords(PickProblem p, {bool camera = true}) => switch (p) {
  PickProblem.refused when camera => 'Taskiem isn’t allowed to use your camera. To take a photo, allow Camera for Taskiem in your phone’s settings.',
  PickProblem.refused => 'Taskiem isn’t allowed to see your photos. To choose one, allow Photos for Taskiem in your phone’s settings.',
  PickProblem.noCamera => 'This phone has no camera Taskiem can use. Choose a photo from your phone instead.',
  PickProblem.failed when camera => 'Your camera couldn’t open. Try again.',
  PickProblem.failed => 'Your photos couldn’t open. Try again.',
};

/// Opens Taskiem's page in the phone's settings (a test gives its own).
Future<void> Function() openPhoneSettings = () async {
  try {
    await stepOut(geo.Geolocator.openAppSettings);
  } on Object {
    // Nothing opened: the words say where to go.
  }
};

/// Says why [e] stopped a photo, as an error toast; refused, with **Open
/// settings** (the phone's settings for Taskiem).
void sayPickProblem(BuildContext context, Object e, {bool camera = true}) {
  if (!context.mounted) return;
  final p = pickProblemOf(e);
  final refused = p == PickProblem.refused;
  showTkToast(
    context,
    pickProblemWords(p, camera: camera),
    error: true,
    action: refused ? 'Open settings' : null,
    onAction: refused ? () => unawaited(openPhoneSettings()) : null,
  );
}
