// The web's parts, drawn the same (web/src/styles.css: button, input,
// label.field, .hint, a). Never Material's: no ripple, no Material shadows.

import 'package:flutter/material.dart' show TextField, InputDecoration, Material, MaterialType;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../theme/theme.dart';

/// A press, as the web's `button:active { transform: scale(0.98) }` with its
/// 80 ms transition: the thing gives a little while held.
class TkPress extends StatefulWidget {
  const TkPress({super.key, required this.child, this.onTap, this.scale = 0.98});

  final Widget child;
  final VoidCallback? onTap;
  final double scale;

  @override
  State<TkPress> createState() => _TkPressState();
}

class _TkPressState extends State<TkPress> {
  bool _down = false;

  void _set(bool v) {
    if (widget.onTap == null || _down == v) return;
    setState(() => _down = v);
  }

  @override
  Widget build(BuildContext context) {
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTapDown: (_) => _set(true),
      onTapUp: (_) => _set(false),
      onTapCancel: () => _set(false),
      onTap: widget.onTap,
      child: AnimatedScale(
        scale: _down && !still ? widget.scale : 1,
        duration: still ? Duration.zero : const Duration(milliseconds: 80),
        curve: Curves.ease,
        child: widget.child,
      ),
    );
  }
}

/// The web's `button`: pine (lime on dark) when [primary], else the panel
/// with a strong border. [minHeight] is 34 on the web, 44 on its sign-in card.
class TkButton extends StatelessWidget {
  const TkButton({
    super.key,
    required this.label,
    this.onPressed,
    this.primary = false,
    this.minHeight = 34,
    this.enabled = true,
  });

  final String label;
  final VoidCallback? onPressed;
  final bool primary;
  final double minHeight;

  /// `button:disabled`: faded to 45%, and nothing happens on a press.
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final fg = primary ? t.primaryText : t.text;
    final box = Container(
      constraints: BoxConstraints(minHeight: minHeight),
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: primary ? t.primary : t.panel,
        border: Border.all(color: primary ? t.primary : t.borderStrong),
        borderRadius: BorderRadius.circular(Tk.radiusSm),
        // `box-shadow: 0 1px 2px rgba(14, 15, 12, 0.12)` on primary; the
        // theme's --shadow on the rest (none in dark).
        boxShadow: primary
            ? const [BoxShadow(offset: Offset(0, 1), blurRadius: 0.87, color: Color(0x1F0E0F0C))]
            : t.shadow,
      ),
      child: Text(
        label,
        textAlign: TextAlign.center,
        style: tkText(size: 13.5, weight: 500, lineHeight: 1.2, color: fg),
      ),
    );
    return Semantics(
      button: true,
      enabled: enabled,
      child: Opacity(
        opacity: enabled ? 1 : 0.45,
        child: TkPress(onTap: enabled ? (onPressed ?? () {}) : null, child: box),
      ),
    );
  }
}

/// The web's `label.field`: its words above a field without an outline, a
/// soft fill that deepens on focus with the ring (styles.css, "Fields
/// without outlines").
class TkField extends StatefulWidget {
  /// Tests that compare screens hide the blinking caret, as the browser's
  /// screenshots do.
  static bool debugHideCaret = false;

  const TkField({
    super.key,
    required this.label,
    this.controller,
    this.autofocus = false,
    this.obscure = false,
    this.keyboard,
    this.autofill,
    this.minHeight = 36,
    this.onSubmitted,
    this.onChanged,
    this.digits,
  });

  final String label;
  final TextEditingController? controller;
  final bool autofocus;
  final bool obscure;
  final TextInputType? keyboard;
  final Iterable<String>? autofill;
  final double minHeight;
  final ValueChanged<String>? onSubmitted;
  final ValueChanged<String>? onChanged;

  /// Only digits, at most this many (a code).
  final int? digits;

  @override
  State<TkField> createState() => _TkFieldState();
}

class _TkFieldState extends State<TkField> {
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(() => setState(() {}));
  }

  @override
  void dispose() {
    _focus.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final focused = _focus.hasFocus;
    final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.only(bottom: 6),
          child: Text(widget.label, style: tkText(size: 13, weight: 500, color: t.text)),
        ),
        // `transition: background-color 0.18s ease, box-shadow 0.18s ease`
        AnimatedContainer(
          duration: still ? Duration.zero : const Duration(milliseconds: 180),
          curve: Curves.ease,
          constraints: BoxConstraints(minHeight: widget.minHeight),
          padding: const EdgeInsets.symmetric(horizontal: 12),
          alignment: Alignment.centerLeft,
          decoration: BoxDecoration(
            color: focused ? t.fieldFocus : t.field,
            borderRadius: BorderRadius.circular(Tk.radiusSm),
            boxShadow: focused ? t.ring : const [],
          ),
          child: Material(
            type: MaterialType.transparency,
            child: TextField(
              controller: widget.controller,
              focusNode: _focus,
              autofocus: widget.autofocus,
              obscureText: widget.obscure,
              keyboardType: widget.keyboard,
              autofillHints: widget.autofill,
              onSubmitted: widget.onSubmitted,
              onChanged: widget.onChanged,
              cursorColor: t.text,
              showCursor: !TkField.debugHideCaret,
              style: tkText(size: 14, color: t.text),
              decoration: const InputDecoration.collapsed(hintText: null),
              inputFormatters: widget.digits == null
                  ? const <TextInputFormatter>[]
                  : [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(widget.digits)],
            ),
          ),
        ),
      ],
    );
  }
}

/// The web's `.hint`: muted, 13 px.
TextStyle tkHint(TkTokens t) => tkText(size: 13, color: t.muted);

/// A link in words (`a`, underlined on the sign-in card), with the web's
/// 4 px above and below so it is easy to press.
class TkLink extends StatelessWidget {
  const TkLink({super.key, required this.label, this.onTap});

  final String label;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      link: true,
      child: GestureDetector(
        behavior: HitTestBehavior.opaque,
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Text(label, style: tkText(size: 13, color: t.accent, decoration: TextDecoration.underline)),
        ),
      ),
    );
  }
}

/// The web's `ErrorBox` (`.error`): the server's words in the danger colour
/// on its faint tint, a thin border, 12 px above and below.
class TkErrorBox extends StatelessWidget {
  const TkErrorBox(this.message, {super.key});

  final String message;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 12),
      child: Semantics(
        liveRegion: true,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 11),
          decoration: BoxDecoration(
            color: cssMix(t.danger, t.panel, 0.06),
            border: Border.all(color: cssMix(t.danger, const Color(0x00000000), 0.25)),
            borderRadius: BorderRadius.circular(10),
          ),
          child: Text(message, style: tkText(size: 14, color: cssMix(t.danger, t.text, 0.82))),
        ),
      ),
    );
  }
}

/// The web's `button.link`: a button that reads as a link (13.5 px,
/// weight 500, the accent colour, underlined), 40 px tall to press, with the
/// button's faint shadow and nothing else.
class TkLinkButton extends StatelessWidget {
  const TkLinkButton({super.key, required this.label, this.onPressed});

  final String label;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      child: TkPress(
        onTap: onPressed ?? () {},
        child: Container(
          constraints: const BoxConstraints(minHeight: 40),
          alignment: Alignment.center,
          decoration: BoxDecoration(borderRadius: BorderRadius.circular(Tk.radiusSm), boxShadow: t.shadow),
          child: Text(label, style: tkText(size: 13.5, weight: 500, lineHeight: 1.2, color: t.accent, decoration: TextDecoration.underline)),
        ),
      ),
    );
  }
}
