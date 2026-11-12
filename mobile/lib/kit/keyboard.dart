// The keyboard's room (as WhatsApp's): when the phone's keyboard comes up,
// every screen and sheet is laid out in the space above it, so a message
// box, a field in a sheet or a sign-in form sits just over the keys
// instead of under them. Material's Scaffold does this for its body; the
// app draws no Scaffold, so this does it once for all of them.

import 'dart:math' as math;

import 'package:flutter/widgets.dart';

class KeyboardRoom extends StatelessWidget {
  const KeyboardRoom({super.key, required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final q = MediaQuery.of(context);
    final keys = q.viewInsets.bottom;
    if (keys <= 0) return child;
    return Padding(
      padding: EdgeInsets.only(bottom: keys),
      child: MediaQuery(
        // Below the screen now is the keyboard, not the phone's own bar: the
        // screens' bottom padding (the gesture bar) is under the keys.
        data: q.copyWith(
          viewInsets: q.viewInsets.copyWith(bottom: 0),
          padding: q.padding.copyWith(bottom: math.max(0, q.padding.bottom - keys)),
        ),
        child: child,
      ),
    );
  }
}
