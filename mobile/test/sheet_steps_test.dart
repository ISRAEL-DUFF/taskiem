// A step inside a sheet (a product's sizes over its form, a bill's "paid
// outside" over the bill; kit.tsx useSheetStep): Back, and the phone's own
// back, come back from the deepest step first, then the sheet's own Back,
// then the sheet closes.

import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:taskiem/kit/kit.dart';
import 'package:taskiem/kit/sheet.dart';
import 'package:taskiem/main.dart';
import 'package:taskiem/theme/theme.dart';

import 'support.dart';

class _Deep extends StatefulWidget {
  const _Deep();

  @override
  State<_Deep> createState() => _DeepState();
}

class _DeepState extends State<_Deep> {
  bool _form = false;
  bool _sizes = false;

  @override
  Widget build(BuildContext context) => TkSheet(
    title: 'Products',
    onBack: _form ? () => setState(() => _form = false) : null,
    body: !_form
        ? TkButton(label: 'Open Asun', onPressed: () => setState(() => _form = true))
        : TkSheetStep(
            open: _sizes,
            onBack: () => setState(() => _sizes = false),
            child: _sizes ? const Text('The sizes') : TkButton(label: 'Sizes', onPressed: () => setState(() => _sizes = true)),
          ),
  );
}

void main() {
  setUpAll(loadFonts);

  testWidgets('Back comes back from the deepest step first, then the sheet’s own, then closes', (t) async {
    t.view.physicalSize = const Size(390, 844);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    await t.pumpWidget(
      TaskiemApp(
        mode: TkMode.light,
        home: Builder(
          builder: (context) => TkButton(label: 'Open', onPressed: () => showTkSheet<void>(context, (_) => const _Deep())),
        ),
      ),
    );
    await t.tap(find.text('Open'));
    await t.pumpAndSettle();
    expect(find.bySemanticsLabel('Back'), findsNothing, reason: 'nothing to go back to');
    await t.tap(find.text('Open Asun'));
    await t.pumpAndSettle();
    await t.tap(find.text('Sizes'));
    await t.pumpAndSettle();
    expect(find.text('The sizes'), findsOneWidget);
    // Back: the form, not the list.
    await t.tap(find.bySemanticsLabel('Back'));
    await t.pumpAndSettle();
    expect(find.text('The sizes'), findsNothing);
    expect(find.text('Sizes'), findsOneWidget);
    // The phone's back: the list.
    await t.binding.handlePopRoute();
    await t.pumpAndSettle();
    expect(find.text('Open Asun'), findsOneWidget);
    // Again: the sheet closes.
    await t.binding.handlePopRoute();
    await t.pumpAndSettle();
    expect(find.text('Products'), findsNothing);
    expect(find.text('Open'), findsOneWidget);
  });
}
