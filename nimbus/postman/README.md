# Postman Collection for Nimbus API

Complete Postman collection for testing the Nimbus Notification API.

---

## 📥 Quick Setup (5 minutes)

### Step 1: Import Collection

1. Open **Postman**
2. Click **Import** (top left)
3. Drag and drop `Nimbus_API.postman_collection.json`
4. Click **Import**

### Step 2: Import Environment

1. Click **Environments** (left sidebar, gear icon)
2. Click **Import**
3. Select `Nimbus_Local.postman_environment.json`
4. Click **Import**

### Step 3: Activate Environment

1. Click the **Environment dropdown** (top right)
2. Select **"Nimbus Local"**

### Step 4: Start Your Server

```bash
cd /Users/lalithlochan/workspace/nimbus
go run ./cmd/gateway
```

### Step 5: Test!

Click on any request in the collection and hit **Send**!

---

## 📁 Collection Structure

```
Nimbus Notification API/
├── Health/
│   └── Health Check
├── Notifications/
│   ├── Create Email Notification
│   ├── Create SMS Notification
│   ├── Create Webhook Notification
│   ├── Get Notification by ID
│   ├── List Notifications (Default)
│   ├── List Notifications (Paginated)
│   ├── Update Status - Mark as Sent
│   ├── Update Status - Mark as Failed
│   └── Update Status - Mark as Processing
└── Error Cases/
    ├── Invalid Tenant ID
    ├── Invalid Channel
    ├── Missing Required Fields
    ├── Invalid Status Update
    └── Non-Existent Notification
```

---

## 🎯 Quick Test Workflow

### 1. Create a Notification
- Select **"Create Email Notification"**
- Click **Send**
- Copy the returned `id` from the response

### 2. Get the Notification
- Select **"Get Notification by ID"**
- In the URL, replace `{{notification_id}}` with the copied ID
  - OR: Set the `notification_id` variable in your environment
- Click **Send**

### 3. Update Status
- Select **"Update Status - Mark as Sent"**
- Make sure `{{notification_id}}` is set
- Click **Send**

### 4. List All Notifications
- Select **"List Notifications (Default)"**
- Click **Send**
- See all your notifications!

---

## 🔧 Environment Variables

The collection uses these variables (already configured):

| Variable | Value | Description |
|----------|-------|-------------|
| `base_url` | `http://localhost:8080` | API base URL |
| `tenant_id` | `00000000-0000-0000-0000-000000000001` | Default tenant UUID |
| `user_id` | `00000000-0000-0000-0000-000000000002` | Default user UUID |
| `notification_id` | (empty) | Set this after creating a notification |

**To change a variable:**
1. Click **Environments** (left sidebar)
2. Select **"Nimbus Local"**
3. Edit the values
4. Click **Save**

---

## 💡 Pro Tips

### Tip 1: Save Notification ID Automatically

Add this **Test Script** to your "Create Email Notification" request:

```javascript
// In the "Tests" tab of the request
if (pm.response.code === 201) {
    var response = pm.response.json();
    pm.environment.set("notification_id", response.id);
    console.log("Saved notification_id: " + response.id);
}
```

Now the `notification_id` will automatically be saved for other requests!

### Tip 2: Add Response Validation

Add this to any request's **Tests** tab:

```javascript
// Check status code
pm.test("Status code is 200", function () {
    pm.response.to.have.status(200);
});

// Check response time
pm.test("Response time is less than 500ms", function () {
    pm.expect(pm.response.responseTime).to.be.below(500);
});

// Check JSON structure
pm.test("Response has id field", function () {
    var jsonData = pm.response.json();
    pm.expect(jsonData).to.have.property('id');
});
```

### Tip 3: Run All Tests at Once

1. Right-click on the collection name
2. Select **"Run collection"**
3. Configure settings
4. Click **"Run Nimbus Notification API"**
5. See all results in one view!

### Tip 4: Share with Team

1. Click **"..."** next to collection name
2. Select **"Export"**
3. Choose **"Collection v2.1"**
4. Share the JSON file with teammates

---

## 🎨 Make It Pretty

### Add Request Descriptions

1. Click on any request
2. Click the **Description** tab
3. Add markdown documentation
4. Great for interviews!

### Organize with Folders

Already done! But you can add more:
- "Smoke Tests"
- "Load Tests"
- "Edge Cases"

---

## 🧪 Testing Scenarios

### Scenario 1: Happy Path
```
1. Health Check ✅
2. Create Email Notification ✅
3. Get Notification by ID ✅
4. List Notifications ✅
5. Update Status to Sent ✅
```

### Scenario 2: Validation Errors
```
1. Invalid Tenant ID → 400 ✅
2. Invalid Channel → 400 ✅
3. Missing Fields → 400 ✅
4. Invalid Status → 400 ✅
```

### Scenario 3: Not Found
```
1. Get Non-Existent Notification → 404 ✅
```

### Scenario 4: Complete Workflow
```
1. Create notification → pending
2. Update to processing
3. Update to sent
4. Verify final status
```

---

## 🚀 Advanced: Pre-request Scripts

Add this to collection or folder level:

```javascript
// Pre-request Script (runs before each request)

// Generate random email
pm.environment.set("random_email", 
    "user" + Math.floor(Math.random() * 10000) + "@example.com"
);

// Log request
console.log("Sending request to: " + pm.request.url);
console.log("Timestamp: " + new Date().toISOString());
```

Then use `{{random_email}}` in your requests!

---

## 📊 Monitoring & Reports

### Export Results

After running the collection:
1. Click **"Export Results"**
2. Save as JSON or HTML
3. Share with team or include in documentation

### Newman (CLI Runner)

Run Postman tests from command line:

```bash
# Install Newman
npm install -g newman

# Run collection
newman run Nimbus_API.postman_collection.json \
  --environment Nimbus_Local.postman_environment.json \
  --reporters cli,html \
  --reporter-html-export report.html
```

---

## 🎤 For Interviews

### Demo Flow (5 minutes)

**Say:** "Let me show you the API I built with Postman"

1. **Health Check** - "First, verify the service is running"
2. **Create Email** - "Here's how we create a notification with validation"
3. **Show Response** - "Notice the clean JSON response with the ID"
4. **Get by ID** - "We can retrieve it by ID"
5. **List All** - "And list all notifications with pagination"
6. **Update Status** - "Workers use this endpoint to mark as sent"
7. **Show Error** - "The API properly validates - see this 400 error"

**Highlight:**
- "I organized requests into logical folders"
- "Error cases are documented"
- "Variables make it easy to switch environments"
- "All validation is working as expected"

---

## 📝 Customization

### Add New Requests

1. Right-click on a folder
2. Select **"Add Request"**
3. Name it
4. Configure method, URL, body
5. Save!

### Create New Environment (Production)

1. Duplicate **"Nimbus Local"**
2. Rename to **"Nimbus Production"**
3. Change `base_url` to production URL
4. Add API keys if needed

---

## ✅ Checklist: Before Interview

- [ ] Collection imported
- [ ] Environment configured
- [ ] Server running
- [ ] Health check works
- [ ] Create/Read/Update/List all work
- [ ] Error cases tested
- [ ] `notification_id` auto-save working
- [ ] Familiar with Postman interface
- [ ] Can explain each endpoint
- [ ] Ready to demo!

---

## 🐛 Troubleshooting

**Problem:** "Request failed"
- ✅ Is server running? (`go run ./cmd/gateway`)
- ✅ Is environment selected? (top right dropdown)
- ✅ Is `base_url` correct? (`http://localhost:8080`)

**Problem:** "Invalid UUID error"
- ✅ Check `tenant_id` and `user_id` in environment
- ✅ Make sure `notification_id` is set (copy from create response)

**Problem:** "404 Not Found"
- ✅ Did you create a notification first?
- ✅ Is the `notification_id` correct?

---

## 📚 Resources

- **Postman Docs:** https://learning.postman.com/
- **Postman Tests:** https://learning.postman.com/docs/writing-scripts/test-scripts/
- **Newman CLI:** https://learning.postman.com/docs/running-collections/using-newman-cli/command-line-integration-with-newman/

---

## 🎯 Next Steps

1. **Add Tests** - Add validation to each request
2. **Create Mock Server** - Postman can generate mock responses
3. **Monitor API** - Set up Postman monitoring (paid feature)
4. **Generate Docs** - Auto-generate API documentation from collection

---

**Happy Testing!** 🚀

If you have questions, check the Postman documentation or ask your interviewer to explore the collection together!
