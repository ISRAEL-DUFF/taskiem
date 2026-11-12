// The web's confirmation and its toasts, as a phone shows them
// (web/src/pages/ConfirmSheet.tsx useConfirm, ui.tsx Modal, toast.tsx;
// styles.css .modal, .modal-backdrop, .toast; motion.css .modal-grab): a
// sheet from the bottom over a dimmed, blurred page asking first — a
// title, what it means, Cancel and the button that agrees (red for what
// cannot be undone) — and a short word after something is done.

import 'dart:async';
import 'dart:ui' as ui;

import 'package:flutter/widgets.dart';

import '../theme/theme.dart';
import 'kit.dart';

/// Opens the web's Modal: [builder] gives what it holds under its title;
/// it closes with what it pops.
Future<T?> showTkModal<T>(BuildContext context, {required String title, required WidgetBuilder builder}) {
  final still = MediaQuery.maybeDisableAnimationsOf(context) ?? false;
  return Navigator.of(context).push<T>(
    PageRouteBuilder<T>(
      opaque: false,
      // `.modal-backdrop { animation: fade-in 0.12s ease-out }`
      transitionDuration: still ? Duration.zero : const Duration(milliseconds: 120),
      reverseTransitionDuration: still ? Duration.zero : const Duration(milliseconds: 120),
      pageBuilder: (context, _, _) => TkModal(
        title: title,
        child: Builder(builder: builder),
      ),
      transitionsBuilder: (context, a, _, child) => FadeTransition(opacity: a, child: child),
    ),
  );
}

/// Asks first; true when the person agrees.
Future<bool> showTkConfirm(BuildContext context, {required String title, String? body, required String ok, bool danger = false}) async {
  final yes = await showTkModal<bool>(
    context,
    title: title,
    builder: (context) {
      final t = TkTheme.tokens(context);
      return Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (body != null) ...[Text(body, style: tkText(size: 14, color: t.text)), const SizedBox(height: 12)],
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              TkPlainButton(label: 'Cancel', onTap: () => Navigator.of(context).pop(false)),
              TkPlainButton(label: ok, danger: danger, primary: !danger, onTap: () => Navigator.of(context).pop(true)),
            ],
          ),
        ],
      );
    },
  );
  return yes ?? false;
}

/// ui.tsx Modal, as a phone shows it.
class TkModal extends StatelessWidget {
  const TkModal({super.key, required this.title, required this.child});
  final String title;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    final pad = MediaQuery.paddingOf(context);
    return Stack(
      children: [
        // `.modal-backdrop { background: rgba(14, 15, 12, 0.32); backdrop-filter: blur(2px) }`
        Positioned.fill(
          child: GestureDetector(
            onTap: () => Navigator.of(context).maybePop(),
            child: BackdropFilter(
              filter: ui.ImageFilter.blur(sigmaX: 2, sigmaY: 2),
              child: const ColoredBox(color: Color(0x520E0F0C)),
            ),
          ),
        ),
        Positioned(
          left: 0,
          right: 0,
          bottom: 0,
          // `.modal { max-height: 92dvh }` on a phone.
          child: ConstrainedBox(
            constraints: BoxConstraints(maxHeight: MediaQuery.sizeOf(context).height * 0.92),
            child: Semantics(
              scopesRoute: true,
              namesRoute: true,
              explicitChildNodes: true,
              label: title,
              child: Container(
                // `.modal` on a phone: the width of the screen, rounded above.
                padding: EdgeInsets.fromLTRB(16, 20, 16, 20 + pad.bottom),
                decoration: BoxDecoration(
                  color: t.panel,
                  border: Border.all(color: t.border),
                  borderRadius: const BorderRadius.vertical(top: Radius.circular(16)),
                  boxShadow: t.shadowLg,
                ),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // `.modal-grab { margin: -12px auto 10px; width: 64px; height: 20px }`:
                    // drawn 12 px up into the padding, 18 px taken in all.
                    SizedBox(
                      height: 18,
                      child: OverflowBox(
                        maxHeight: 20,
                        alignment: Alignment.topCenter,
                        child: Transform.translate(
                          offset: const Offset(0, -12),
                          child: GestureDetector(
                            behavior: HitTestBehavior.opaque,
                            onVerticalDragEnd: (d) {
                              if ((d.primaryVelocity ?? 0) > 200) Navigator.of(context).maybePop();
                            },
                            child: Container(
                              width: 64,
                              height: 20,
                              padding: const EdgeInsets.fromLTRB(14, 8, 14, 8),
                              child: Container(
                                decoration: BoxDecoration(color: t.borderStrong, borderRadius: BorderRadius.circular(999)),
                              ),
                            ),
                          ),
                        ),
                      ),
                    ),
                    Semantics(
                      header: true,
                      child: Text(title, style: tkText(size: 15, weight: 600, lineHeight: 1.35, color: t.text)),
                    ),
                    const SizedBox(height: 12),
                    Flexible(child: SingleChildScrollView(child: child)),
                  ],
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }
}

/// The web's plain `button` in a row of actions: 13.5 px, a pill with a
/// line round it, 36 px tall; `.primary` filled, `.danger` red words.
class TkPlainButton extends StatelessWidget {
  const TkPlainButton({super.key, required this.label, this.onTap, this.primary = false, this.danger = false});
  final String label;
  final VoidCallback? onTap;
  final bool primary;
  final bool danger;

  @override
  Widget build(BuildContext context) {
    final t = TkTheme.tokens(context);
    return Semantics(
      button: true,
      label: label,
      excludeSemantics: true,
      child: TkPress(
        onTap: onTap,
        child: IntrinsicWidth(
          child: Container(
            constraints: const BoxConstraints(minHeight: 36),
            padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 6),
            alignment: Alignment.center,
            decoration: BoxDecoration(
              color: primary ? t.primary : t.panel,
              border: Border.all(color: primary ? t.primary : t.borderStrong),
              borderRadius: BorderRadius.circular(999),
            ),
            child: Text(label, style: tkText(size: 13.5, weight: 500, lineHeight: 1.2, color: primary ? t.primaryText : (danger ? t.danger : t.text))),
          ),
        ),
      ),
    );
  }
}

/// toast.tsx: a short word after something is done, over the bottom of the
/// screen for 4 seconds (an error stays until closed), × to close it.
void showTkToast(BuildContext context, String text, {bool error = false}) {
  final overlay = Overlay.of(context, rootOverlay: true);
  late OverlayEntry entry;
  Timer? timer;
  void close() {
    timer?.cancel();
    if (entry.mounted) entry.remove();
  }

  entry = OverlayEntry(
    builder: (context) {
      final t = TkTheme.tokens(context);
      final pad = MediaQuery.paddingOf(context);
      return Positioned(
        // `@media (max-width: 860px) { .toasts { bottom: 76px; left: 16px; right: 16px } }`
        left: 16,
        right: 16,
        bottom: 76 + pad.bottom,
        child: Semantics(
          liveRegion: true,
          // `.toast { border: 1px solid; border-left: 4px solid }`, rounded:
          // Flutter draws no rounded border of two colours, so the line is
          // what shows round the panel laid in it.
          child: DecoratedBox(
            decoration: BoxDecoration(borderRadius: BorderRadius.circular(Tk.radiusSm), boxShadow: t.shadowLg),
            child: ClipRRect(
              borderRadius: BorderRadius.circular(Tk.radiusSm),
              child: Container(
                color: t.borderStrong,
                child: Stack(
                  children: [
                    Positioned(left: 0, top: 0, bottom: 0, width: 4, child: ColoredBox(color: error ? t.danger : t.ok)),
                    Container(
                      margin: const EdgeInsets.fromLTRB(4, 1, 1, 1),
                      padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
                      decoration: BoxDecoration(
                        color: t.panel,
                        borderRadius: BorderRadius.horizontal(
                          left: Radius.elliptical(Tk.radiusSm - 4, Tk.radiusSm - 1),
                          right: Radius.circular(Tk.radiusSm - 1),
                        ),
                      ),
                      child: Row(
                        children: [
                          Expanded(
                            child: Text(text, style: tkText(size: 14, color: t.text)),
                          ),
                          const SizedBox(width: 10),
                          Semantics(
                            button: true,
                            label: 'Dismiss',
                            excludeSemantics: true,
                            child: GestureDetector(
                              onTap: close,
                              child: SizedBox(
                                width: 28,
                                height: 28,
                                child: Center(
                                  child: Text('×', style: tkText(size: 18, lineHeight: 1, color: t.muted)),
                                ),
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      );
    },
  );
  overlay.insert(entry);
  if (!error) timer = Timer(const Duration(seconds: 4), close);
}

/// A step whose screen or tool the app has not got yet (it comes with the
/// tools, docs/spec/mobile-app.md M5 to M8): shown as the web shows it,
/// and said plainly when pressed, never a press that does nothing.
void showComingSoon(BuildContext context) => showTkToast(context, 'This is coming to the app soon.');
