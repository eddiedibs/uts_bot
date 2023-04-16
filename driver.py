#!/home/edd1e/scripts/projs/uts_bot/uts_bot_env/bin/python3

import configuration as conf
import getpass

from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.common.by import By
from selenium.webdriver.common.keys import Keys


class Browser:

    browser, service = None, None
    def __init__(self, driver:str):
        web_options = webdriver.ChromeOptions() 
        web_options.add_argument("--remote-debugging-port=9222")  # thisself.service = Service(driver)
        web_options.add_experimental_option("detach", True)
        self.browser = webdriver.Chrome(service=self.service,options=web_options)
        

    def open_page(self,url:str):
        self.browser.get(url)

    def click_button(self, by_param:str, element:str):
        button = self.browser.find_element(by_param, element)
        button.click()

    def type_data(self, data:dict):
        email_input = input("ENTER EMAIL>> ")
        self.browser.find_element(By.NAME, data["LOGIN"]).send_keys(email_input + Keys.ENTER)
        password = getpass.getpass('ENTER PASSWORD>> ')
        self.browser.find_element(By.NAME, data["PASS"]).send_keys(password + Keys.ENTER)
    def close_browser(self):
        self.browser.close()



if __name__ == "__main__":

    browser = Browser(conf.CHROME_DRIVER_DIR)
    browser.open_page(conf.saia_page)
    browser.click_button(By.CLASS_NAME, "login-identityprovider-btn")
    browser.type_data({"LOGIN":"loginfmt", "PASS": "passwd"})
    browser.click_button(By.ID,"idBtn_Back")
    browser.click_button(By.LINK_TEXT, "https://saia2.uts.edu.ve/my/courses.php")
    # browser.close_browser()