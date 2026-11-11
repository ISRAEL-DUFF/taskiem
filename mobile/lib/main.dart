// Taskiem's chat as a phone app (docs/spec/mobile-app.md): the web chat,
// drawn and moving the same.

import 'package:flutter/material.dart' show MaterialApp;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import 'screens/sign_in.dart';
import 'theme/theme.dart';

void main() {
  // The bars over the app take the web's `theme-color`: pine, light icons.
  SystemChrome.setSystemUIOverlayStyle(const SystemUiOverlayStyle(
    statusBarColor: Color(0x00000000),
    statusBarIconBrightness: Brightness.light,
    statusBarBrightness: Brightness.dark,
  ));
  runApp(const TaskiemApp());
}

class TaskiemApp extends StatelessWidget {
  const TaskiemApp({super.key, this.mode = TkMode.light, this.home});

  final TkMode mode;

  /// The first screen (tests show one screen alone).
  final Widget? home;

  @override
  Widget build(BuildContext context) {
    // MaterialApp only for what text fields need (localizations, selection);
    // nothing on screen is Material's.
    return MaterialApp(
      title: 'Taskiem',
      debugShowCheckedModeBanner: false,
      builder: (context, child) => TkTheme(
        mode: mode,
        // The web's body: Inter 14 px, line height 1.55, the text colour.
        child: Builder(
          builder: (context) => DefaultTextStyle(
            style: tkText(size: 14, color: TkTheme.tokens(context).text).copyWith(decoration: TextDecoration.none),
            child: child!,
          ),
        ),
      ),
      home: home ?? const SignInScreen(),
    );
  }
}
