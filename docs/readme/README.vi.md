# nexus-trade-bot

<p align="center">
  <img src="../../logo/logo.png" alt="Nexus Trade Bot" width="720">
</p>

**Một trung tâm điều khiển bot dạng lưới được xây dựng dành cho các nhà giao dịch muốn khối lượng, tự động hóa và khả năng hiển thị rủi ro mà không cần trông chừng mọi đơn hàng. Hợp đồng tương lai là chế độ mặc định; lưới giao ngay được hỗ trợ trên các sàn giao dịch tập trung lớn.**

[![Go](https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-green)](../../LICENSE)
[![One Command](https://img.shields.io/badge/install-one%20command-blue)](#cài-đặt-một-lệnh)
[![Languages](https://img.shields.io/badge/languages-11-orange)](#ngôn-ngữ)

## Tham gia nhóm người dùng

Các câu hỏi triển khai, chi tiết API sàn, kinh nghiệm giao dịch thật và phản hồi phiên bản mới sẽ được xử lý nhanh hơn khi người dùng ở cùng một nơi. Tham gia nhóm người dùng Nexus Trade Bot: [https://t.me/nexustradebot8](https://t.me/nexustradebot8).

## Ngôn ngữ

[English](../../README.md) | [简体中文](README.zh-CN.md) | [Русский](README.ru.md) | [한국어](README.ko.md) | [日本語](README.ja.md) | [Español](README.es.md) | Tiếng Việt | [हिन्दी](README.hi.md) | [Português](README.pt.md) | [العربية](README.ar.md) | [繁體中文](README.zh-TW.md)

## Đọc phần này trước

Nếu bạn là người dùng thông thường, hãy bắt đầu bằng cài đặt một lệnh, mở bảng điều khiển web, thêm API chỉ có quyền giao dịch và không có quyền rút tiền, rồi chạy bot thử nghiệm với số vốn nhỏ.

Nếu bạn là nhà phát triển, hãy build từ mã nguồn, xem `config.example.yaml`, chạy `go test ./...`, sau đó dùng worker mode khi muốn chạy bot bằng một file cấu hình cụ thể.

README này đi từ phần thực dụng đến kỹ thuật: cài đặt, sàn hỗ trợ, tính năng, ví dụ chiến lược, tham số, cài đặt thủ công và kiểm tra trước khi giao dịch thật.

## Cài đặt một lệnh

Chạy cái này trên máy chủ Ubuntu mới:

```bash
wget -O nexus-trade-bot.sh https://raw.githubusercontent.com/haohaoi34/nexus-trade-bot/main/scripts/nexus-trade-bot.sh && chmod +x nexus-trade-bot.sh && ./nexus-trade-bot.sh install && ./nexus-trade-bot.sh start
```

Trình chạy máy chủ tự động:

- Cài đặt phần phụ thuộc Ubuntu bị thiếu.
- Cài đặt Go nếu máy chủ không có phiên bản tương thích.
- Sao chép `https://github.com/haohaoi34/nexus-trade-bot.git` khi nó chưa chạy trong quá trình kiểm tra nguồn.
- Xây dựng bot từ nguồn hoặc sử dụng tệp nhị phân đi kèm trong gói phát hành.
- Tạo `config.yaml` từ `config.example.yaml` nếu cần và giữ nó cục bộ.
- Khởi động bảng điều khiển web ở chế độ nền và ghi nhật ký vào `logs/`.
- Tự động phát hiện IP công khai của máy chủ và in một khối truy cập nổi bật với URL cục bộ, URL máy chủ, tệp PID và đường dẫn log.

Các lệnh máy chủ hữu ích:

```bash
./nexus-trade-bot.sh install
./nexus-trade-bot.sh start
./nexus-trade-bot.sh status
./nexus-trade-bot.sh logs
./nexus-trade-bot.sh restart
./nexus-trade-bot.sh stop
./nexus-trade-bot.sh update
```

Đăng nhập web mặc định:

```text
username: admin
password: admin
```

Thay đổi mật khẩu mặc định ngay sau lần đăng nhập đầu tiên.

## Sàn giao dịch được hỗ trợ

- Binance ☑️
- Bitget ☑️
- Gate.io ☑️
- Bybit ☑️
- OKX ☑️
- Hyperliquid ☑️

Liên kết hoàn phí Bitget: [hoàn phí tới 70%, mã mời `4n9z`](https://partner.hdmune.cn/bg/3DLRKF).


## Nó làm gì

nexus-trade-bot giúp bạn chạy các chiến lược lưới từ bảng điều khiển web sạch:

- Thêm API trao đổi một lần và xác minh chúng trước khi sử dụng.
- Tạo nhiều bot cho các biểu tượng, tài khoản và chỉ đường khác nhau.
- Chọn tương lai hoặc giao ngay. Hợp đồng tương lai được chọn theo mặc định.
- Sử dụng chế độ mua, bán hoặc trung tính cho hợp đồng tương lai; sử dụng chế độ dài tại chỗ.
- Tự động tải các biểu tượng giao ngay Binance, Bitget, Bybit, OKX, Gate và Hyperliquid.
- Xem số dư, khối lượng giao dịch, trạng thái bot và PnL trong thời gian thực.
- Tạm dừng bot, thay đổi tham số và khởi động lại bot với cài đặt mới nhất.
- Hãy để người theo dõi rủi ro ngừng giao dịch khi thị trường có những biến động bất thường.

Nó được thiết kế dành cho những nhà giao dịch quan tâm đến việc khớp lệnh, doanh thu và kiểm soát, không dành cho những người muốn chỉnh sửa tệp cấu hình cả ngày.

## Ý tưởng cốt lõi

Robot lưới đặt lệnh mua và bán ở những khoảng giá cố định. Thay vì cố gắng dự đoán chính xác đỉnh hoặc đáy, nó tiếp tục hoạt động xung quanh một phạm vi giá:

- Khi giá giảm, bot sẽ mua dần dần theo cài đặt lưới của bạn.
- Khi giá tăng trở lại, bot sẽ bán từng mức cao hơn.
- Trong một thị trường đi ngang hoặc phục hồi đi lên, điều này có thể biến sự biến động thành các giao dịch được thực hiện lặp đi lặp lại.
- Trong xu hướng giảm một chiều, bot tích lũy vị thế và cần có đủ tiền ký quỹ, giới hạn rủi ro và sự kiên nhẫn.

Mục tiêu không phải là lợi nhuận kỳ diệu. Mục tiêu là thực hiện có kỷ luật: khoảng cách lệnh nhất quán, quy mô lệnh được kiểm soát, rủi ro có thể nhìn thấy và phản ứng tự động khi thị trường trở nên bất thường.


## Chiến lược ví dụ: Lưới ETH có doanh thu cao

Đây là một ví dụ thực tế để hiểu cách các nhà giao dịch sử dụng loại bot này.

Giả sử ETH đang giao dịch gần `3000` và bạn định cấu hình:

| Tham số | Ví dụ |
| --- | --- |
| Biểu tượng | `ETHUSDT` hoặc `ETHUSDC` |
| Hướng | Lưới dài |
| Khoảng giá | `1 USDT` |
| Số tiền đặt hàng | `300 USDT` mỗi đơn hàng lưới |
| Phong cách thị trường | Thị trường đi ngang hoặc phục hồi đi lên |

Với khoảng thời gian `1 USDT` chặt chẽ và tính thanh khoản ETH tích cực, bot có thể tạo ra doanh thu rất cao. Trong một thị trường bận rộn, loại cấu hình này có thể đạt tới hàng triệu đô la về khối lượng giao dịch hàng ngày và hàng chục triệu về khối lượng hàng tháng, tùy thuộc vào sự biến động, phí, tính thanh khoản và quy mô tài khoản.

Đây là lý do tại sao nhiều nhà giao dịch sử dụng hệ thống lưới cho hai mục đích:

- **Xây dựng khối lượng**: tăng khối lượng giao dịch tương lai cho các chiến dịch hoặc cấp VIP trao đổi.
- **Thu hoạch biến động**: liên tục mua thấp hơn và bán cao hơn trong một phạm vi.


## Ví dụ về logic rút tiền

Giao dịch lưới phải được lên kế hoạch xung quanh việc rút tiền.

Giả sử ETH bắt đầu gần `3000` và rơi xuống `2700`. Một lưới dài thường sẽ chịu một khoản lỗ thả nổi vì nó đã mua trên đường đi xuống. Nhưng nó cũng đã tích lũy các mục thấp hơn. Nếu sau đó giá tăng trở lại từ `2700` về `2850`, thì chi phí trung bình có thể được kéo xuống đủ để tài khoản đạt đến mức hòa vốn sớm hơn một lần nhập tại `3000`.

Nếu ETH trở lại gần khu vực `3000` ban đầu, chiến lược có thể được hưởng lợi từ cả hai:

- phục hồi hàng tồn kho từ sự phục hồi;
- chênh lệch lưới thực hiện được thu thập trong quá trình di chuyển.

Một số nhà giao dịch dự trữ bộ đệm ký quỹ lớn hơn, chẳng hạn như xung quanh `30,000 USDT`, để thiết kế một lưới có thể chịu đựng được một động thái sâu hơn nhiều chẳng hạn như rút xuống `1000 USDT` ETH. Liệu điều đó có đủ hay không phụ thuộc vào đòn bẩy, chế độ ký quỹ, quy mô vị thế, phí, quy tắc ký quỹ duy trì trao đổi và mức độ tích cực của mạng lưới của bạn.

Điểm quan trọng: lợi nhuận lưới điện đến từ sự chuẩn bị chứ không phải sự lạc quan. Trước khi chạy kích thước, hãy tính toán xem thị trường có thể di chuyển theo hướng bất lợi cho bạn bao xa, bot có thể tích lũy được bao nhiêu vị trí và điều gì sẽ xảy ra nếu thị trường không phục hồi nhanh chóng.


## Bảo vệ rủi ro tích hợp

Giảm nhanh một chiều là môi trường tồi tệ nhất đối với lưới điện dài hung hãn. nexus-trade-bot bao gồm một công cụ giám sát rủi ro thị trường được thiết kế để giảm thiểu vấn đề này:

- theo dõi các biểu tượng chính như BTC, ETH, SOL, XRP và DOGE;
- phát hiện hành vi giá và khối lượng bất thường;
- tạm dừng giao dịch khi điều kiện thị trường trở nên nguy hiểm;
- chỉ cho phép giao dịch lại sau khi phục hồi đủ các biểu tượng được theo dõi.

Điều này không loại bỏ rủi ro, nhưng nó mang lại cho bot cơ hội ngừng thêm rủi ro trong các động thái thanh lý đột ngột.


## Những cách phổ biến để sử dụng nó

### 1. Xây dựng cấp độ VIP và số lượng

Sử dụng khoảng thời gian chặt chẽ và kích thước lệnh được kiểm soát trên các biểu tượng có tính thanh khoản cao. Mục tiêu là doanh thu cao với khả năng thực hiện có thể dự đoán được. Ở đây, mức phí rất quan trọng, vì vậy hãy sử dụng các cặp phí thấp hoặc chương trình giảm giá nếu có thể.

### 2. Lưới dài sau khi thị trường thoái lui

Bắt đầu sau một đợt giảm giá có ý nghĩa thay vì chạy theo một đợt bơm thẳng đứng. Bot mua theo lớp và bán theo đợt hồi phục. Phong cách này cần có đủ lợi nhuận để tồn tại trong những đợt thoái lui sâu hơn.

### 3. Lưới giao ngay Binance

Sử dụng chế độ giao ngay khi bạn muốn bot mua và bán tiền thật thay vì mở các vị thế tương lai có đòn bẩy. Chế độ giao ngay chỉ có hiệu lực trong thời gian dài: bot mua mức thấp hơn trước và bán hàng tồn kho để phục hồi. Nó đơn giản hơn hợp đồng tương lai nhưng vẫn cần có đủ số dư báo giá và kế hoạch cho các xu hướng giảm kéo dài.

### 4. Thoát kho

Nếu bạn đã giữ một vị thế, bot có thể giúp bán dần vị thế đó khi giá tăng. Khi vị trí giảm hoàn toàn, bạn có thể dừng bot.

### 5. Lưới trung tính

Sử dụng chế độ trung tính khi bạn muốn cả hoạt động của lưới theo chiều dài và chiều ngắn. Bắt đầu với kích thước nhỏ hơn và xem cách sàn giao dịch xử lý chế độ vị trí trước khi mở rộng quy mô.


## Hướng dẫn tham số

| Cài đặt | Ý nghĩa của nó | Mẹo thực tế |
| --- | --- | --- |
| `symbol` | Cặp giao dịch | Bắt đầu với các cặp thanh khoản như BTC hoặc ETH. |
| `app.market_type` | `futures` hoặc `spot` | Mặc định là `futures`. Giao dịch trực tiếp giao ngay hỗ trợ Binance, Bitget, Bybit, OKX, Gate và Hyperliquid thông qua các bộ chuyển đổi chuyên dụng. |
| `direction` | `long`, `short` hoặc `neutral` | Lưới dài cần ký quỹ để rút tiền. Lưới ngắn không được vô tình áp dụng vị trí bán thủ công không liên quan trừ khi bạn cố tình kích hoạt hành vi đó. |
| `price_interval` | Khoảng cách giữa các cấp lưới | Khoảng thời gian nhỏ hơn có nghĩa là nhiều giao dịch hơn và nhiều phí hơn. |
| `order_quantity` | Số tiền sử dụng cho mỗi đơn hàng | Số tiền lớn hơn làm tăng doanh thu và rút tiền. Xác nhận xem giao diện người dùng đang hiển thị giá trị báo giá hay số lượng cơ bản cho loại thị trường và sàn giao dịch của bạn. |
| `min_order_value` | Danh nghĩa đơn hàng tối thiểu | Phải đáp ứng mức trao đổi tối thiểu. |
| `risk_control.enabled` | Bảo vệ sự bất thường của thị trường | Hãy luôn kích hoạt nó trừ khi bạn biết chính xác lý do tại sao không. |


## Bảng điều khiển web

Bảng điều khiển hỗ trợ 11 ngôn ngữ:

Tiếng Anh, tiếng Trung giản thể, tiếng Nga, tiếng Hàn, tiếng Nhật, tiếng Tây Ban Nha, tiếng Việt, tiếng Hindi, tiếng Bồ Đào Nha, tiếng Ả Rập và tiếng Trung phồn thể.

Chế độ Bảng điều khiển Web hiển thị:

- quản lý API
- tạo và chỉnh sửa bot
- trao đổi logo
- số dư thời gian thực
- hôm nay và tổng PnL thực hiện
- hôm nay và tổng khối lượng giao dịch
- trạng thái bot đang chạy, tạm dừng và dừng


## Cài đặt thủ công

```bash
git clone https://github.com/haohaoi34/nexus-trade-bot.git
cd nexus-trade-bot
go mod download
go build -o nexus-trade-bot .
```

Khởi động Bảng điều khiển Web:

```bash
./nexus-trade-bot
```

URL cục bộ mặc định:

```text
http://127.0.0.1:8080
```

Hiển thị trên máy chủ:

```bash
NEXUS_TRADE_BOT_ADDR=0.0.0.0:8080 ./nexus-trade-bot
```

Trình chạy máy chủ một lệnh từ kiểm tra nguồn:

```bash
chmod +x scripts/nexus-trade-bot.sh
scripts/nexus-trade-bot.sh install
scripts/nexus-trade-bot.sh start
scripts/nexus-trade-bot.sh status
scripts/nexus-trade-bot.sh logs
scripts/nexus-trade-bot.sh stop
```

Trình chạy hoạt động từ cả gói kiểm tra nguồn và gói phát hành. Ở chế độ nguồn, nó xây dựng `./nexus-trade-bot`; trong chế độ phát hành, nó sử dụng trực tiếp tệp nhị phân đi kèm.

Chạy chế độ công nhân CLI:

```bash
./nexus-trade-bot worker config.yaml
```


## Trước khi bạn giao dịch trực tiếp

Hãy kiểm tra những điều này trước:

- Khóa API có quyền giao dịch nhưng không có quyền rút tiền.
- Chế độ ký quỹ là những gì bạn mong đợi.
- Đòn bẩy không quá tích cực.
- Biểu tượng có đủ thanh khoản.
- Kích thước đặt hàng đáp ứng mức tối thiểu trao đổi.
- Bạn hiểu lưới có thể tích lũy được bao nhiêu vị trí.
- Bạn có kế hoạch cho thị trường một chiều.
- Tường lửa máy chủ của bạn chỉ hiển thị cổng web khi có ý định.


## Tuyên bố từ chối trách nhiệm

Giao dịch tương lai có thể gây ra tổn thất đáng kể. Chiến lược lưới có thể hoạt động tốt trong các thị trường có giới hạn phạm vi hoặc đang phục hồi, nhưng chúng cũng có thể tích lũy các vị thế lớn trong các xu hướng một chiều mạnh mẽ. nexus-trade-bot là phần mềm thực thi; bạn chịu trách nhiệm về cài đặt chiến lược, cấu hình trao đổi, rủi ro tài khoản và mọi giao dịch được thực hiện thông qua khóa API của mình.
