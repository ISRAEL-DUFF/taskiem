// Every test file under test/: the motions that never end (a live dot's
// pulse) are off, so a screen settles (pumpAndSettle). test/motion_test.dart
// turns them on where it checks them.

import 'dart:async';

import 'package:taskiem/kit/motion.dart';

Future<void> testExecutable(FutureOr<void> Function() testMain) async {
  tkRepeatingMotion = false;
  await testMain();
}
